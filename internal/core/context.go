package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// What an agent uses on the developer's machine must also reach it in a Loop: its own skills, plugins,
// subagents, commands, user instructions and the project's memory (the CLI loads most of it itself; read access
// lets the agent open a skill's scripts and references, and the project's memory from a worktree), plus the
// read-only paths a Loop lists (readPaths) and the Git-ignored files it copies into a worktree (copyFiles).

// claudeProjectKey is the folder name Claude Code keeps a project's sessions and memory under
// (~/.claude/projects/<key>): the absolute path with every character other than a letter or digit replaced by "-".
func claudeProjectKey(dir string) string {
	return nonAlnum.ReplaceAllString(dir, "-")
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// credentialStores are folders that hold the computer's general-purpose credentials. A Loop may not list them
// as read paths: an agent that reads issues or web pages could be talked into sending them away. Credentials a
// Loop needs belong in its env (system keychain) or in a folder for that purpose.
var credentialStores = []string{".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker", ".netrc", ".git-credentials",
	filepath.Join(".config", "gh"), filepath.Join(".config", "gcloud"), filepath.Join("Library", "Keychains")}

// expandHome turns "~/x" (or "~\x") into an absolute path under home.
func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(home, p[2:])
	}
	return p
}

// validateReadPath checks one entry of Loop.ReadPaths without touching the disk.
func validateReadPath(p, home string) error {
	raw := strings.TrimSpace(p)
	if raw == "" || raw != p {
		return fmt.Errorf("read path %q: remove the surrounding spaces or the empty entry", p)
	}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return fmt.Errorf("read path %s must not contain ..", p)
		}
	}
	abs := expandHome(raw, home)
	if !filepath.IsAbs(abs) {
		return fmt.Errorf("read path %s must be absolute or start with ~/", p)
	}
	abs = filepath.Clean(abs)
	if (home != "" && within(home, abs)) || filepath.Dir(abs) == abs {
		return fmt.Errorf("read path %s is too broad; name the folder the agent needs", p)
	}
	if home != "" {
		for _, s := range credentialStores {
			store := filepath.Join(home, s)
			if within(abs, store) || within(store, abs) {
				return fmt.Errorf("read path %s would expose %s, which holds this computer's credentials; put what the Loop needs in its environment variables (stored in the system keychain) instead", p, "~/"+filepath.ToSlash(s))
			}
		}
	}
	return nil
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		path, dir = strings.ToLower(path), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// resolveReadPaths expands and checks a Loop's read paths at run time; a missing path blocks the Run.
func resolveReadPaths(l Loop, home string) ([]string, error) {
	var out []string
	for _, p := range l.ReadPaths {
		if err := validateReadPath(p, home); err != nil {
			return nil, err
		}
		abs := filepath.Clean(expandHome(p, home))
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("read path %s does not exist on this computer; create it or remove it from the Loop's read paths", p)
		}
		out = append(out, abs)
	}
	return out, nil
}

// validateCopyFile checks one entry of Loop.CopyFiles: a path relative to the project, inside it.
func validateCopyFile(p string) error {
	if strings.TrimSpace(p) == "" || strings.TrimSpace(p) != p {
		return fmt.Errorf("copied file %q: remove the surrounding spaces or the empty entry", p)
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || filepath.VolumeName(p) != "" || strings.HasPrefix(p, "~") {
		return fmt.Errorf("copied file %s must be relative to the project folder", p)
	}
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return fmt.Errorf("copied file %s must stay inside the project folder", p)
		}
		if strings.EqualFold(part, ".git") {
			return fmt.Errorf("copied file %s: Git metadata is never copied", p)
		}
	}
	if c := filepath.ToSlash(filepath.Clean(p)); c == "." {
		return errors.New("copied files must name files or folders, not the whole project")
	}
	return nil
}

// claudeReadRule is a Claude Code permission rule allowing reads of p ("//" marks an absolute path; Windows
// paths use the /c/Users/... form).
func claudeReadRule(p string, dir bool) string {
	s := filepath.ToSlash(p)
	if v := filepath.VolumeName(p); len(v) == 2 && v[1] == ':' {
		s = "/" + strings.ToLower(v[:1]) + filepath.ToSlash(p[2:])
	}
	s = "/" + s
	if dir {
		s = strings.TrimSuffix(s, "/") + "/**"
	}
	return "Read(" + s + ")"
}

// readablePrompt tells the agent which paths outside the working directory it may read.
func readablePrompt(paths []string, memory string) string {
	if len(paths) == 0 && memory == "" {
		return ""
	}
	var b strings.Builder
	if len(paths) > 0 {
		b.WriteString("\n\nBesides the working directory you may read (not change) these paths, which the Loop's owner listed for this task:\n")
		for _, p := range paths {
			b.WriteString("- " + p + "\n")
		}
	}
	if memory != "" {
		b.WriteString("\n\nThe project's memory from the developer's own sessions is in " + memory + " (read it when it helps; MEMORY.md is the index).")
	}
	return strings.TrimRight(b.String(), "\n")
}
