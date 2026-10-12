package core

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// workspace is where a Run executes. In worktree mode it is a fresh git worktree under the data directory, so agents
// never switch branches or collide with uncommitted work in the developer's own checkout.
type workspace struct {
	// Reviewed is the commit up to which the reviewer has been shown the change; the next diff starts there.
	Reviewed string
	Dir      string
	Branch   string // branch Loopbee created and commits to ("" = detached or direct mode)
	Base     string // commit the worktree started from
	gitFile  []byte // <Dir>/.git right after creation; a changed file means the agent tampered with it
	hooks    string // empty hooks directory for Loopbee's own Git commands
}

// prepareWorkspace creates the Run's workspace. Agents get no write access to Git metadata: with l.Branch set,
// Loopbee creates the branch and commits for them (Codex's sandbox forbids .git writes, and writable hooks or
// config would let an agent run code outside its sandbox through Loopbee's own Git commands).
func (a *App) prepareWorkspace(ctx context.Context, l Loop, runID string, issue int) (*workspace, error) {
	if l.Workspace != "worktree" {
		return &workspace{Dir: l.Directory}, nil
	}
	root := filepath.Join(a.Dir, "worktrees")
	hooks := filepath.Join(a.Dir, "no-hooks")
	for _, d := range []string{root, hooks} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(root, l.ID)
	git := func(args ...string) ([]byte, error) {
		return runCommand(ctx, l.Directory, append([]string{"git"}, args...), nil)
	}
	if _, err := git("rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, errors.New("worktree mode requires the working directory to be a Git repository")
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
	continued := false
	add := []string{"worktree", "add", "--detach", path, base}
	if l.Branch != "" {
		branch = expandBranch(l.Branch, runID, time.Now(), issue)
		if out, err := git("check-ref-format", "--branch", branch); err != nil {
			return nil, fmt.Errorf("invalid branch name %s: %s", branch, strings.TrimSpace(string(out)))
		}
		add = []string{"worktree", "add", "-b", branch, path, base}
		if _, err := git("rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			if issue == 0 {
				return nil, fmt.Errorf("branch %s already exists; merge or delete it, or put {date} in the Loop's branch name", branch)
			}
			// A retry of the same issue (after a blocked Run or a sent-back review): continue its branch, so earlier
			// work is not lost. A branch without commits of its own is simply started again from the base.
			if out, err := git("rev-list", "--count", base+".."+branch); err == nil && strings.TrimSpace(string(out)) == "0" {
				add = []string{"worktree", "add", "-B", branch, path, base}
			} else {
				add = []string{"worktree", "add", path, branch}
				continued = true
			}
		}
	}
	if out, err := git(add...); err != nil {
		msg := fmt.Sprintf("could not create worktree from %s: %s", base, strings.TrimSpace(string(out)))
		if _, e := git("rev-parse", "--verify", "--quiet", base+"^{commit}"); e != nil {
			// The usual cause: a base like origin/master in a repository whose default branch is main.
			if names := remoteBranches(git); len(names) > 0 {
				msg = fmt.Sprintf("the base %s does not exist in this repository; set the Loop's base to one of: %s", base, strings.Join(names, ", "))
			}
		}
		return nil, errors.New(msg)
	}
	a.event(runID, "workspace.worktree", path+" @ "+base+map[bool]string{true: " → " + branch, false: ""}[branch != ""])
	w := &workspace{Dir: path, Branch: branch, hooks: hooks}
	w.gitFile, _ = os.ReadFile(filepath.Join(path, ".git"))
	out, err := w.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("worktree has no commit: %s", strings.TrimSpace(string(out)))
	}
	w.Base = strings.TrimSpace(string(out))
	if continued {
		// Everything since the base counts as the agent's work: command steps refuse it and the reviewer sees it.
		out, err := w.git(ctx, "merge-base", base, "HEAD")
		if err != nil {
			return nil, fmt.Errorf("cannot continue %s from %s: %s", branch, base, strings.TrimSpace(string(out)))
		}
		w.Base = strings.TrimSpace(string(out))
		a.event(runID, "workspace.continue", fmt.Sprintf("%s already has work from an earlier Run; continuing it", branch))
	}
	w.Reviewed = w.Base
	return w, nil
}

// remoteBranches lists remote-tracking branches as "origin/main". It reads full ref names because Git's short form
// varies by version (Git 2.46+ shows the remote's HEAD pointer as a bare "origin"), and skips those HEAD pointers.
func remoteBranches(git func(args ...string) ([]byte, error)) []string {
	out, err := git("for-each-ref", "--format=%(refname)", "refs/remotes/")
	if err != nil {
		return nil
	}
	var names []string
	for _, ref := range strings.Fields(string(out)) {
		name := strings.TrimPrefix(ref, "refs/remotes/")
		if strings.HasSuffix(name, "/HEAD") || !strings.Contains(name, "/") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// git runs one of Loopbee's own Git commands in a workspace an agent may have changed: never with hooks, an
// fsmonitor or an external diff, and only while the worktree still points at its real Git directory.
func (w *workspace) git(ctx context.Context, args ...string) ([]byte, error) {
	return w.gitEnv(ctx, nil, args...)
}

// gitEnv is git with extra environment variables.
func (w *workspace) gitEnv(ctx context.Context, env []string, args ...string) ([]byte, error) {
	if w.gitFile != nil {
		if b, err := os.ReadFile(filepath.Join(w.Dir, ".git")); err != nil || !bytes.Equal(b, w.gitFile) {
			msg := "the worktree's .git file was changed during the run; Loopbee will not run Git there"
			return []byte(msg), errors.New(msg)
		}
	}
	safe := []string{"git", "-c", "core.hooksPath=" + w.hooks, "-c", "core.fsmonitor=false", "-c", "core.pager=cat"}
	// GIT_TERMINAL_PROMPT=0: a push that needs credentials fails instead of waiting for input forever.
	return runCommandEnv(ctx, w.Dir, append(safe, args...), nil, nil, append([]string{"GIT_TERMINAL_PROMPT=0"}, env...))
}

// changed lists files that differ from the commit the Run started from: committed, staged, modified or new.
func (w *workspace) changed(ctx context.Context) (map[string]bool, error) {
	set := map[string]bool{}
	for _, args := range [][]string{{"diff", "--no-ext-diff", "--name-only", w.Base}, {"ls-files", "--others", "--exclude-standard"}} {
		out, err := w.git(ctx, args...)
		if err != nil {
			return nil, fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
		}
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f = strings.TrimSpace(f); f != "" {
				set[f] = true
			}
		}
	}
	return set, nil
}

// hasWork reports whether the branch holds commits since the Run's base (on a continued branch: earlier Runs' work).
func (w *workspace) hasWork(ctx context.Context) bool {
	out, err := w.git(ctx, "rev-list", "--count", w.Base+"..HEAD")
	return err == nil && strings.TrimSpace(string(out)) != "0"
}

// changedFileIn returns the first argument of argv that names a file changed during the Run, or "".
func (w *workspace) changedFileIn(argv []string, changed map[string]bool) string {
	for _, arg := range argv {
		p := arg
		if !filepath.IsAbs(p) {
			p = filepath.Join(w.Dir, p)
		}
		rel, err := filepath.Rel(w.Dir, p)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		if changed[filepath.ToSlash(rel)] {
			return filepath.ToSlash(rel)
		}
	}
	return ""
}

// expandBranch fills {date}, {time}, {run} and {issue} in a branch template.
func expandBranch(tpl, runID string, now time.Time, issue int) string {
	short := runID
	if len(short) > 8 {
		short = short[:8]
	}
	return strings.NewReplacer("{date}", now.Format("20060102"), "{time}", now.Format("1504"), "{run}", short, "{issue}", strconv.Itoa(issue)).Replace(tpl)
}

// commit commits every change in the workspace and returns the short hash, or "" when nothing changed. It runs as
// Loopbee, outside the agent's sandbox, so it never runs hooks: the agent may have written them (e.g. .husky/).
func (w *workspace) commit(ctx context.Context, message string) (string, error) {
	out, err := w.git(ctx, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("git status: %s", strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return "", nil
	}
	if out, err := w.git(ctx, "add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %s", strings.TrimSpace(string(out)))
	}
	identity := []string{}
	if out, _ := w.git(ctx, "config", "user.email"); strings.TrimSpace(string(out)) == "" {
		identity = []string{"-c", "user.name=Loopbee", "-c", "user.email=loopbee@localhost"}
	}
	if out, err := w.git(ctx, append(identity, "commit", "-q", "--no-verify", "-m", message)...); err != nil {
		return "", fmt.Errorf("git commit: %s", tail(string(out), 600))
	}
	out, err = w.git(ctx, "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(string(out)), err
}

// diffSecretPattern finds credentials in added lines. Narrower than secretPattern: code legitimately contains
// e-mail addresses and the words "token" or "password".
var diffSecretPattern = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----|\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}|\bgh[pousr]_[A-Za-z0-9]{30,}|\bgithub_pat_[A-Za-z0-9_]{30,}|\bAKIA[0-9A-Z]{16}\b|\bxox[abprs]-[A-Za-z0-9-]{10,}`)

// secretsInPatch returns "file: kind" for every added line in a unified diff that looks like a credential.
func secretsInPatch(patch []byte) []string {
	var found []string
	file := ""
	for _, line := range strings.Split(string(patch), "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
		case strings.HasPrefix(line, "+") && diffSecretPattern.MatchString(line):
			m := diffSecretPattern.FindString(line)
			if len(m) > 12 {
				m = m[:12] + "…"
			}
			found = append(found, file+": "+m)
		}
	}
	return found
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
