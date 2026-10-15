package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"loopbee/internal/testenv"
)

func bareTeamRemote(t *testing.T) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "team.git")
	if out, err := runCommand(context.Background(), "", []string{"git", "init", "-q", "--bare", remote}, nil); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return remote
}

func teamCommits(t *testing.T, remote string) int {
	n, _ := strconv.Atoi(gitOutput(t, remote, "rev-list", "--count", "loopbee"))
	return n
}

func eventually(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("not within %s: %s", within, what)
}

// The team saw stale boards whenever the executor forgot “Publish now”. Setting the repository publishes at once;
// after that every change is published by itself, several changes close together in one commit.
func TestTeamPublishesWithoutTheButton(t *testing.T) {
	teamPublishDelay = 300 * time.Millisecond
	t.Cleanup(func() { teamPublishDelay = 30 * time.Second })
	a := testApp(t)
	remote := bareTeamRemote(t)
	c := a.Settings()
	c.TeamRemote = remote
	if err := a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "published when the repository was set", func() bool { return teamCommits(t, remote) == 1 })
	for _, name := range []string{"Nightly report", "Fix tests", "Docs sweep"} {
		l := testLoop(t)
		l.Name = name
		if _, err := a.SaveLoop(l); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, 10*time.Second, "published after the Loops were saved", func() bool { return teamCommits(t, remote) == 2 })
	time.Sleep(3 * teamPublishDelay)
	if n := teamCommits(t, remote); n != 2 {
		t.Fatalf("%d commits; three saves close together must make one", n)
	}
	file := gitOutput(t, remote, "show", "loopbee:loopbee/"+c.Publisher+".json")
	if !strings.Contains(file, "Docs sweep") {
		t.Fatalf("the last save is not in the published snapshot:\n%.300s", file)
	}
	s, _ := a.State()
	if s.Team.Last == nil || s.Team.Error != "" || s.Team.Pending {
		t.Fatalf("team status %+v", s.Team)
	}
}

// Every publisher pushes its own file to one branch; when another one pushed between fetch and push, the push is
// rejected and the sync starts over from the new branch instead of failing.
func TestTeamPublishStartsOverWhenAnotherPublisherPushed(t *testing.T) {
	a := testApp(t)
	remote := bareTeamRemote(t)
	c := a.Settings()
	c.TeamRemote = remote
	if err := a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "first publish", func() bool { return teamCommits(t, remote) == 1 })
	other := filepath.Join(t.TempDir(), "other")
	git := func(dir string, args ...string) {
		cmd := append([]string{"git", "-c", "user.name=other", "-c", "user.email=other@localhost"}, args...)
		if out, err := runCommand(context.Background(), dir, cmd, nil); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("", "clone", "-q", "--branch", "loopbee", remote, other)
	beforeTeamPush = func() {
		beforeTeamPush = nil
		os.WriteFile(filepath.Join(other, "loopbee", "other.json"), []byte(`{"brand":"loopbee","loops":[],"runs":[]}`), 0600)
		git(other, "add", ".")
		git(other, "commit", "-q", "-m", "Other publisher")
		git(other, "push", "-q", "origin", "loopbee")
	}
	t.Cleanup(func() { beforeTeamPush = nil })
	l := testLoop(t)
	l.Name = "After the other publisher"
	if _, err := a.SaveLoop(l); err != nil {
		t.Fatal(err)
	}
	if err := a.SyncTeam(); err != nil {
		t.Fatalf("publish after another publisher pushed: %v", err)
	}
	files := gitOutput(t, remote, "ls-tree", "--name-only", "loopbee", "loopbee/")
	if !strings.Contains(files, "loopbee/other.json") || !strings.Contains(gitOutput(t, remote, "show", "loopbee:loopbee/"+c.Publisher+".json"), "After the other publisher") {
		t.Fatalf("both publishers' files must be on the branch: %s", files)
	}
}

// Syncs run in the background: a remote that asks for a password fails at once instead of waiting for an answer
// nobody can give.
func TestTeamSyncNeverWaitsForCredentials(t *testing.T) {
	testenv.Loopback(t)
	if runtime.GOOS == "windows" {
		t.Skip("the waiting credential helper is a shell script")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="team"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	ask := filepath.Join(t.TempDir(), "ask.sh")
	os.WriteFile(ask, []byte("#!/bin/sh\nsleep 120\n"), 0700)
	t.Setenv("GIT_ASKPASS", ask)
	t.Setenv("SSH_ASKPASS", ask)
	a := testApp(t)
	c := a.Settings()
	c.TeamRemote = srv.URL + "/team.git"
	start := time.Now()
	if err := a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	eventually(t, 20*time.Second, "the sync failed instead of waiting for a password", func() bool {
		s, _ := a.State()
		return s.Team.Error != ""
	})
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("waited %s", d)
	}
}

// A viewer pulls when the repository is set and then every few minutes, without “Pull now”.
func TestViewerPullsByItself(t *testing.T) {
	teamPullEvery = 200 * time.Millisecond
	t.Cleanup(func() { teamPullEvery = 5 * time.Minute })
	a := testApp(t)
	remote := bareTeamRemote(t)
	c := a.Settings()
	c.TeamRemote = remote
	if err := a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	first := testLoop(t)
	first.Name = "First"
	if _, err := a.SaveLoop(first); err != nil {
		t.Fatal(err)
	}
	if err := a.SyncTeam(); err != nil {
		t.Fatal(err)
	}
	b := testApp(t)
	c = b.Settings()
	c.Role, c.TeamRemote = "viewer", remote
	if err := b.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	shared := func() int {
		s, _ := b.State()
		return len(s.Loops)
	}
	eventually(t, 10*time.Second, "pulled when the repository was set", func() bool { return shared() == 1 })
	b.serveTeam()
	second := testLoop(t)
	second.Name = "Second"
	if _, err := a.SaveLoop(second); err != nil {
		t.Fatal(err)
	}
	if err := a.SyncTeam(); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "pulled the executor's next publish", func() bool { return shared() == 2 })
}
