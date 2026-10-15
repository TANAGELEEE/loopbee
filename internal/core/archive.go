package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var secretPattern = regexp.MustCompile(`(?i)(-----BEGIN [A-Z ]*PRIVATE KEY-----|\bsk-[a-z0-9_-]{16,}|\bgh[pousr]_[a-z0-9]{20,}|(?:api[_-]?key|password|secret|token)["']?\s*[=:]\s*["']?[^\s"']{8,}|\b[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}\b)`)

func sensitive(data []byte) bool {
	if secretPattern.Match(data) {
		return true
	}
	var v any
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	return sensitiveValue(v, 0)
}
func sensitiveValue(v any, depth int) bool {
	if depth > 32 {
		return true
	}
	switch value := v.(type) {
	case string:
		if secretPattern.MatchString(value) {
			return true
		}
		var inner any
		if json.Unmarshal([]byte(value), &inner) == nil {
			return sensitiveValue(inner, depth+1)
		}
	case []any:
		for _, entry := range value {
			if sensitiveValue(entry, depth+1) {
				return true
			}
		}
	case map[string]any:
		for k, entry := range value {
			if s, ok := entry.(string); ok && secretPattern.MatchString(k+"="+s) {
				return true
			}
			if sensitiveValue(entry, depth+1) {
				return true
			}
		}
	}
	return false
}

func (a *App) Prune() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role == "viewer" {
		return 0, errors.New("viewer is read-only")
	}
	runs, err := a.Runs()
	if err != nil {
		return 0, err
	}
	count := 0
	cutoff := time.Now().AddDate(0, 0, -a.config.RetentionDays)
	for _, r := range runs {
		if !terminal(r.Status) || r.Finished == nil || r.Finished.After(cutoff) {
			continue
		}
		if filepath.Base(r.ID) != r.ID || r.ID == "." {
			return count, errors.New("invalid run id")
		}
		tx, err := a.db.Begin()
		if err != nil {
			return count, err
		}
		if _, err = tx.Exec("DELETE FROM events WHERE run_id=?", r.ID); err != nil {
			tx.Rollback()
			return count, err
		}
		if _, err = tx.Exec("DELETE FROM objects WHERE kind='run' AND id=?", r.ID); err != nil {
			tx.Rollback()
			return count, err
		}
		if err = tx.Commit(); err != nil {
			return count, err
		}
		if err = os.RemoveAll(filepath.Join(a.Dir, "runs", r.ID)); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (a *App) SyncTeam() error {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	c := a.Settings()
	if c.TeamRemote == "" || strings.HasPrefix(c.TeamRemote, "-") {
		return errors.New("team Git remote required")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9/_-]*$`).MatchString(c.TeamBranch) || strings.Contains(c.TeamBranch, "..") {
		return errors.New("invalid team branch")
	}
	// Clone into an app-owned checkout; never switch branches in the user's repository.
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	dir := filepath.Join(a.Dir, "team")
	git := func(args ...string) ([]byte, error) {
		out, err := runCommand(ctx, dir, append([]string{"git", "-c", "core.hooksPath=", "-c", "protocol.ext.allow=never"}, args...), nil)
		if err != nil {
			return nil, fmt.Errorf("Git %s failed; check remote access and branch", args[0])
		}
		return out, nil
	}
	// The checkout is loopbee's own. A different repository starts a fresh one, so the old repository's history and
	// other publishers' files never reach the new one.
	if remote, err := git("remote", "get-url", "origin"); err == nil && strings.TrimSpace(string(remote)) != c.TeamRemote {
		if err = os.RemoveAll(dir); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if _, err = git("init"); err != nil {
			return err
		}
		if _, err = git("remote", "add", "origin", c.TeamRemote); err != nil {
			return err
		}
	}
	refs, err := git("ls-remote", "--heads", "origin", c.TeamBranch)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(refs))) > 0 {
		if _, err = git("fetch", "origin", c.TeamBranch); err != nil {
			return err
		}
		if _, err = git("checkout", "-B", c.TeamBranch, "FETCH_HEAD"); err != nil {
			return err
		}
	} else {
		if c.Role == "viewer" {
			return errors.New("team branch has not been published")
		}
		if _, err = git("checkout", "-B", c.TeamBranch); err != nil {
			return err
		}
	}
	if c.Role == "viewer" {
		return a.readTeam(dir)
	}
	local, err := a.State()
	if err != nil {
		return err
	}
	// Publish only what the team reads; settings, agents, Info (data directory), updates and gh stay on this computer.
	s := State{Brand: local.Brand, Updated: local.Updated, Evidence: map[string]Evidence{}}
	home, _ := os.UserHomeDir()
	paths := map[string]string{a.Dir: "<data>", home: "~"}
	loops := []Loop{}
	runs := []Run{}
	publicLoops := map[string]bool{}
	for _, l := range local.Loops {
		publicLoops[l.ID] = !l.Private
		if !l.Private {
			paths[l.Directory] = "<project>"
			l.Directory = ""
			loops = append(loops, l)
		}
	}
	for _, r := range local.Runs {
		if !r.Definition.Private && publicLoops[r.LoopID] {
			if _, ok := paths[r.Workdir]; !ok {
				paths[r.Workdir] = "<workdir>"
			}
			paths[r.Definition.Directory] = "<project>"
			r.Definition.Directory = ""
			r.Workdir = ""
			evidence := Evidence{Artifacts: map[string]string{}}
			if evidence.Events, err = a.Events(r.ID); err != nil {
				return err
			}
			for _, name := range r.Artifacts {
				data, err := a.ReadArtifact(r.ID, name)
				if err != nil {
					return err
				}
				if sensitive(data) {
					return errors.New("publish blocked: sensitive artifact")
				}
				evidence.Artifacts[name] = string(data)
			}
			s.Evidence[r.ID] = evidence
			runs = append(runs, r)
		}
	}
	s.Loops = loops
	s.Runs = runs
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = withoutLocalPaths(b, paths)
	if len(b) > 16<<20 {
		return errors.New("team snapshot exceeds 16 MiB; archive and prune old runs first")
	}
	if sensitive(b) {
		return errors.New("publish blocked: possible secret detected")
	}
	sub := filepath.Join(dir, "loopbee")
	if err = os.MkdirAll(sub, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(sub)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("team directory must not be a symlink")
	}
	file := filepath.Join(sub, c.Publisher+".json")
	if info, err = os.Lstat(file); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("team file must not be a symlink")
	}
	if err = os.WriteFile(file, b, 0600); err != nil {
		return err
	}
	if _, err = git("add", "--", "loopbee/"+c.Publisher+".json"); err != nil {
		return err
	}
	changes, err := git("diff", "--cached", "--name-only")
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	if _, err = git("-c", "user.name=loopbee", "-c", "user.email=loopbee@localhost", "commit", "-m", "Update loopbee dashboard"); err != nil {
		return err
	}
	_, err = git("push", "origin", "HEAD:refs/heads/"+c.TeamBranch)
	return err
}

// withoutLocalPaths replaces this computer's folders in a JSON snapshot (event text, artifacts, reports, a
// definition's read paths) with placeholders, so "<project>/go.mod" still says which file was meant. Longer paths
// go first, so a worktree inside the data directory becomes <workdir>, not <data>/worktrees/….
func withoutLocalPaths(b []byte, paths map[string]string) []byte {
	type root struct{ path, name string }
	roots := []root{}
	for path, name := range paths {
		if len(path) < 2 || !filepath.IsAbs(path) {
			continue
		}
		// As it appears in a JSON string, and inside JSON text that was itself stored as a string.
		once, _ := json.Marshal(path)
		twice, _ := json.Marshal(string(once[1 : len(once)-1]))
		roots = append(roots, root{string(twice[1 : len(twice)-1]), name}, root{string(once[1 : len(once)-1]), name})
	}
	sort.Slice(roots, func(i, j int) bool { return len(roots[i].path) > len(roots[j].path) })
	pairs := []string{}
	for _, r := range roots {
		pairs = append(pairs, r.path, r.name)
	}
	return []byte(strings.NewReplacer(pairs...).Replace(string(b)))
}

func (a *App) readTeam(dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "loopbee", "*.json"))
	if err != nil {
		return err
	}
	states := []State{}
	var total int64
	for _, file := range files {
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return errors.New("invalid team snapshot")
		}
		total += info.Size()
		if total > 64<<20 {
			return errors.New("team snapshots exceed 64 MiB")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var s State
		if err = json.Unmarshal(b, &s); err != nil {
			return err
		}
		for _, l := range s.Loops {
			if !regexp.MustCompile(`^[a-f0-9]{24}$`).MatchString(l.ID) {
				return errors.New("invalid shared loop ID")
			}
		}
		for _, r := range s.Runs {
			if !regexp.MustCompile(`^[a-f0-9]{24}$`).MatchString(r.ID) {
				return errors.New("invalid shared run ID")
			}
			for _, name := range r.Artifacts {
				if !validArtifactName.MatchString(name) {
					return errors.New("invalid shared artifact name")
				}
				if _, ok := s.Evidence[r.ID].Artifacts[name]; !ok {
					return errors.New("shared artifact missing")
				}
			}
		}
		states = append(states, s)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role != "viewer" {
		return errors.New("role changed during sync")
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Shared data has its own namespace so changing roles never destroys local runs.
	if _, err = tx.Exec("DELETE FROM objects WHERE kind IN ('shared_loop','shared_run','shared_evidence')"); err != nil {
		return err
	}
	for _, s := range states {
		for _, l := range s.Loops {
			b, _ := json.Marshal(l)
			if _, err = tx.Exec("INSERT INTO objects(kind,id,data) VALUES('shared_loop',?,?)", l.ID, string(b)); err != nil {
				return err
			}
		}
		for _, r := range s.Runs {
			b, _ := json.Marshal(r)
			if _, err = tx.Exec("INSERT INTO objects(kind,id,data) VALUES('shared_run',?,?)", r.ID, string(b)); err != nil {
				return err
			}
			evidence, _ := json.Marshal(s.Evidence[r.ID])
			if _, err = tx.Exec("INSERT INTO objects(kind,id,data) VALUES('shared_evidence',?,?)", r.ID, string(evidence)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
