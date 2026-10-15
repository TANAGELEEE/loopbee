package core

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// projectName names the project a Loop works on, so boards can group and filter by it. The name is published with
// the team snapshot, so it must be the same on every teammate's computer and never contain a local path: the
// owner/repo of the directory's origin remote, or else the folder name.
func projectName(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := runCommand(ctx, dir, []string{"git", "remote", "get-url", "origin"}, nil); err == nil {
		if name := remoteProject(strings.TrimSpace(string(out))); name != "" {
			return name
		}
	}
	return filepath.Base(dir)
}

// remoteProject is the owner/repo part of a Git remote URL, without user, token or host; "" for a local path.
func remoteProject(remote string) string {
	var path string
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil || u.Scheme == "file" {
			return ""
		}
		path = u.Path
	} else if host, rest, ok := strings.Cut(remote, ":"); ok && len(host) > 1 && !strings.ContainsAny(host, `/\`) {
		path = rest // scp-like: git@github.com:owner/repo.git (a Windows drive letter has a one-letter "host")
	} else {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if len(parts) < 2 || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

func validProject(p string) error {
	if len(p) > 80 || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return errors.New("project must be a short name on one line (maximum 80 characters)")
	}
	return nil
}
