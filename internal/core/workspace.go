package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// prepareWorkspace returns the directory a Run executes in, plus extra writable directories.
// In worktree mode each Run gets a fresh detached git worktree under the data directory, so agents
// never switch branches or collide with uncommitted work in the developer's own checkout.
// With l.Branch set, the worktree checks out a new branch of that name instead of a detached HEAD.
func (a *App) prepareWorkspace(ctx context.Context, l Loop, runID string) (string, []string, string, error) {
	if l.Workspace != "worktree" {
		return l.Directory, nil, "", nil
	}
	root := filepath.Join(a.Dir, "worktrees")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", nil, "", err
	}
	path := filepath.Join(root, l.ID)
	git := func(args ...string) ([]byte, error) {
		return runCommand(ctx, l.Directory, append([]string{"git"}, args...), nil)
	}
	if _, err := git("rev-parse", "--is-inside-work-tree"); err != nil {
		return "", nil, "", errors.New("worktree mode requires the working directory to be a Git repository")
	}
	if _, err := os.Stat(path); err == nil {
		git("worktree", "remove", "--force", path)
		os.RemoveAll(path)
	}
	git("worktree", "prune")
	base := l.BaseRef
	if base == "" {
		base = "HEAD"
	}
	if remote, _, ok := strings.Cut(base, "/"); ok && base != "HEAD" {
		// Best effort: refresh the remote branch the worktree starts from.
		if out, err := git("fetch", "--quiet", remote); err != nil {
			a.event(runID, "workspace.fetch_failed", strings.TrimSpace(string(out)))
		}
	}
	branch := ""
	add := []string{"worktree", "add", "--detach", path, base}
	if l.Branch != "" {
		branch = expandBranch(l.Branch, runID, time.Now())
		if out, err := git("check-ref-format", "--branch", branch); err != nil {
			return "", nil, "", fmt.Errorf("invalid branch name %s: %s", branch, strings.TrimSpace(string(out)))
		}
		if _, err := git("rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			return "", nil, "", fmt.Errorf("branch %s already exists; merge or delete it, or put {date} in the Loop's branch name", branch)
		}
		add = []string{"worktree", "add", "-b", branch, path, base}
	}
	if out, err := git(add...); err != nil {
		return "", nil, "", fmt.Errorf("could not create worktree from %s: %s", base, strings.TrimSpace(string(out)))
	}
	a.event(runID, "workspace.worktree", path+" @ "+base+map[bool]string{true: " → " + branch, false: ""}[branch != ""])
	extra := []string{}
	if out, err := runCommand(ctx, path, []string{"git", "rev-parse", "--path-format=absolute", "--git-common-dir"}, nil); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			extra = append(extra, d) // commits and branches in a worktree write to the main repository's .git
		}
	}
	return path, extra, branch, nil
}

// expandBranch fills {date}, {time} and {run} in a branch template.
func expandBranch(tpl, runID string, now time.Time) string {
	short := runID
	if len(short) > 8 {
		short = short[:8]
	}
	return strings.NewReplacer("{date}", now.Format("20060102"), "{time}", now.Format("1504"), "{run}", short).Replace(tpl)
}

// commitWorkspace commits every change in dir (a Loopbee worktree) and returns the short hash, or "" when
// nothing changed. It runs as Loopbee, outside the agent's sandbox, and keeps the repository's hooks.
func commitWorkspace(ctx context.Context, dir, message string) (string, error) {
	git := func(args ...string) ([]byte, error) {
		return runCommand(ctx, dir, append([]string{"git"}, args...), nil)
	}
	out, err := git("status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("git status: %s", strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return "", nil
	}
	if out, err := git("add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %s", strings.TrimSpace(string(out)))
	}
	identity := []string{}
	if out, _ := git("config", "user.email"); strings.TrimSpace(string(out)) == "" {
		identity = []string{"-c", "user.name=Loopbee", "-c", "user.email=loopbee@localhost"}
	}
	if out, err := git(append(identity, "commit", "-q", "-m", message)...); err != nil {
		return "", fmt.Errorf("git commit: %s", tail(string(out), 600))
	}
	out, err = git("rev-parse", "--short", "HEAD")
	return strings.TrimSpace(string(out)), err
}

// loopEnv resolves the Loop's environment variables, reading secrets from the keychain.
func loopEnv(l Loop) ([]string, error) {
	env := []string{"LOOPBEE_LOOP=" + l.Name}
	for _, e := range l.Env {
		if e.Value == "" {
			return nil, fmt.Errorf("environment variable %s has no value; set it in the Loop editor", e.Name)
		}
		v, err := resolveSecret(e.Value)
		if err != nil {
			return nil, fmt.Errorf("environment variable %s is unavailable: %w", e.Name, err)
		}
		env = append(env, e.Name+"="+v)
	}
	return env, nil
}

// storeLoopSecrets moves plaintext secret values into the system keychain and keeps references only.
func (a *App) storeLoopSecrets(l Loop) ([]EnvVar, error) {
	out := make([]EnvVar, len(l.Env))
	for i, e := range l.Env {
		out[i] = e
		if !e.Secret || e.Value == "" || strings.HasPrefix(e.Value, "keyring:") || strings.HasPrefix(e.Value, "env:") {
			continue
		}
		ref, err := storeSecret(a.config.Publisher, "loop/"+newID()+"/"+e.Name, e.Value)
		if err != nil {
			return nil, err
		}
		out[i].Value = ref
	}
	return out, nil
}

// adoptLoginPath merges the PATH of the user's login shell into this process. Apps started from the
// Dock or Finder inherit a minimal PATH and would not find agents installed through nvm or Homebrew.
func adoptLoginPath() {
	if runtime.GOOS == "windows" {
		return
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
		if _, err := os.Stat(shell); err != nil {
			shell = "/bin/sh"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	out, err := runCommand(ctx, "", []string{shell, "-ilc", `printf '\n__LOOPBEE_PATH__%s\n' "$PATH"`}, nil)
	if err != nil && len(out) == 0 {
		return
	}
	login := ""
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "__LOOPBEE_PATH__"); ok {
			login = v
		}
	}
	if login == "" {
		return
	}
	os.Setenv("PATH", mergePath(os.Getenv("PATH"), login))
}

func mergePath(current, login string) string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range []string{login, current} {
		for _, p := range filepath.SplitList(list) {
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}
