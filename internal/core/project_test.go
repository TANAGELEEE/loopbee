package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The project is published with the team snapshot: it keeps owner/repo and never a user, token, host or local path.
func TestRemoteProjectKeepsOnlyOwnerAndRepo(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:acme/api.git":                     "acme/api",
		"https://github.com/acme/api":                     "acme/api",
		"https://user:ghp_secret@github.com/acme/api.git": "acme/api",
		"ssh://git@gitlab.example.com:2222/group/sub/web": "sub/web",
		"/Users/someone/repos/api.git":                    "",
		"file:///Users/someone/repos/api.git":             "",
		`C:\repos\api.git`:                                "",
		"../api.git":                                      "",
		"https://github.com/acme":                         "",
	} {
		if got := remoteProject(remote); got != want {
			t.Errorf("remoteProject(%q) = %q, want %q", remote, got, want)
		}
	}
}

// Teammates' checkouts live at different paths, so a Loop's project comes from the origin remote; without one it is
// the folder name, and a name the person typed is kept.
func TestLoopProjectComesFromTheRemoteOrFolder(t *testing.T) {
	a := testApp(t)
	withRemote := testLoop(t)
	git := func(args ...string) {
		if out, err := runCommand(context.Background(), withRemote.Directory, append([]string{"git"}, args...), nil); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("remote", "add", "origin", "https://user:ghp_secret@github.com/acme/api.git")
	l, err := a.SaveLoop(withRemote)
	if err != nil {
		t.Fatal(err)
	}
	if l.Project != "acme/api" {
		t.Fatalf("project from the remote = %q, want acme/api", l.Project)
	}
	plain := testLoop(t)
	if l, err = a.SaveLoop(plain); err != nil || l.Project != filepath.Base(plain.Directory) {
		t.Fatalf("project without a remote = %q (%v), want the folder name", l.Project, err)
	}
	named := testLoop(t)
	named.Project = "  Docs site "
	if l, err = a.SaveLoop(named); err != nil || l.Project != "Docs site" {
		t.Fatalf("typed project = %q (%v), want it kept", l.Project, err)
	}
	named.Project = strings.Repeat("x", 81)
	if _, err = a.SaveLoop(named); err == nil {
		t.Fatal("an 81-character project was accepted")
	}
}

// Loops saved by an earlier version get their project when loopbee starts, without a new version.
func TestLoopsSavedBeforeProjectsGetOne(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := testLoop(t)
	old.ID, old.Version = newID(), 3
	if err = a.put("loop", old.ID, old); err != nil {
		t.Fatal(err)
	}
	a.Close()
	if a, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var l Loop
	if err = a.get("loop", old.ID, &l); err != nil {
		t.Fatal(err)
	}
	if l.Project != filepath.Base(old.Directory) || l.Version != 3 {
		t.Fatalf("after start: project %q version %d, want %q and 3", l.Project, l.Version, filepath.Base(old.Directory))
	}
}
