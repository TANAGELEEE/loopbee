package core

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"loopbee/internal/update"
)

// releaseFeed serves version as the newest GitHub release, with a Windows archive whose loopbee.exe says body.
func releaseFeed(t *testing.T, version, body string) {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	w, _ := z.Create("loopbee.exe")
	w.Write([]byte(body))
	z.Close()
	archive := buf.Bytes()
	name := "loopbee-windows-" + runtime.GOARCH + ".zip"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "latest":
			json.NewEncoder(rw).Encode(map[string]any{"tag_name": "v" + version, "html_url": "https://example.invalid/v" + version, "body": "What changed",
				"assets": []map[string]any{{"name": name, "browser_download_url": srv.URL + "/dl/" + name, "size": len(archive)},
					{"name": "SHA256SUMS", "browser_download_url": srv.URL + "/dl/SHA256SUMS"}}})
		case "SHA256SUMS":
			sum := sha256.Sum256(archive)
			fmt.Fprintf(rw, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		default:
			rw.Write(archive)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LOOPBEE_UPDATE_FEED", srv.URL+"/latest")
}

// updateSandbox makes this a Windows desktop app of version current, installed as exe, whose restarts are
// recorded instead of done.
func updateSandbox(t *testing.T, current string) (exe string, restarts, relaunched *atomic.Int32) {
	t.Helper()
	dir := t.TempDir()
	exe = filepath.Join(dir, "loopbee.exe")
	os.WriteFile(exe, []byte("old"), 0o755)
	restarts, relaunched = new(atomic.Int32), new(atomic.Int32)
	oldVersion, oldGOOS, oldExe, oldRelaunch, oldFirst, oldTick, oldClient := Version, updateGOOS, updateExecutable, updateRelaunch, updateFirst, updateWaitTick, updateClient
	Version, updateGOOS = current, "windows"
	updateExecutable = func() (string, error) { return exe, nil }
	updateRelaunch = func(update.Target) error { relaunched.Add(1); return nil }
	updateFirst, updateWaitTick = time.Hour, 10*time.Millisecond
	updateClient = func() *update.Client { return &update.Client{} }
	t.Cleanup(func() {
		Version, updateGOOS, updateExecutable, updateRelaunch, updateFirst, updateWaitTick, updateClient = oldVersion, oldGOOS, oldExe, oldRelaunch, oldFirst, oldTick, oldClient
	})
	return exe, restarts, relaunched
}

func waitUpdate(t *testing.T, a *App, state string) UpdateStatus {
	t.Helper()
	for i := 0; i < 500; i++ {
		if s := a.UpdateState(); s.State == state {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("update state %+v, want %s", a.UpdateState(), state)
	return UpdateStatus{}
}

// The whole path, after magpie's updater: a newer release is downloaded and checked at once, a restart asked for
// while a Run is active waits for it to end, then puts the new version in, starts it and quits.
func TestUpdateDownloadsAndRestartsWhenIdle(t *testing.T) {
	exe, restarts, relaunched := updateSandbox(t, "0.1.2")
	releaseFeed(t, "0.1.3", "new loopbee")
	a := testApp(t)
	a.EnableUpdates(func() { restarts.Add(1) })
	a.CheckUpdate()
	s := waitUpdate(t, a, "ready")
	if s.Latest != "0.1.3" || s.Current != "0.1.2" || s.Notes != "What changed" || !s.CanRestart {
		t.Fatalf("%+v", s)
	}
	st, _ := a.State()
	if st.Update.State != "ready" {
		t.Fatalf("state carries the update: %+v", st.Update)
	}
	a.mu.Lock()
	a.active["run1"] = func() {}
	a.mu.Unlock()
	if s, err := a.RestartToUpdate(""); err != nil || !s.Waiting || s.Active != 1 {
		t.Fatalf("%+v %v", s, err)
	}
	time.Sleep(50 * time.Millisecond)
	if restarts.Load() != 0 {
		t.Fatal("restarted while a Run was active")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatal("installed while a Run was active")
	}
	a.mu.Lock()
	delete(a.active, "run1")
	a.mu.Unlock()
	for i := 0; i < 200 && restarts.Load() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if restarts.Load() != 1 || relaunched.Load() != 1 {
		t.Fatalf("restarts %d relaunched %d", restarts.Load(), relaunched.Load())
	}
	if b, _ := os.ReadFile(exe); string(b) != "new loopbee" {
		t.Fatalf("installed %q", b)
	}
}

func TestUpdateStates(t *testing.T) {
	t.Run("source builds never update", func(t *testing.T) {
		updateSandbox(t, "0.1.0-dev")
		releaseFeed(t, "0.1.3", "new")
		a := testApp(t)
		a.EnableUpdates(func() {})
		a.CheckUpdate()
		if s := waitUpdate(t, a, "source"); s.Latest != "0.1.3" {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("up to date", func(t *testing.T) {
		updateSandbox(t, "0.1.3")
		releaseFeed(t, "0.1.3", "new")
		a := testApp(t)
		a.EnableUpdates(func() {})
		a.CheckUpdate()
		waitUpdate(t, a, "latest")
	})
	t.Run("headless only tells", func(t *testing.T) {
		exe, _, _ := updateSandbox(t, "0.1.2")
		releaseFeed(t, "0.1.3", "new")
		a := testApp(t)
		a.EnableUpdates(nil)
		a.CheckUpdate()
		if s := waitUpdate(t, a, "available"); s.Stuck != "headless" || s.CanRestart || s.URL == "" {
			t.Fatalf("%+v", s)
		}
		if _, err := a.RestartToUpdate("now"); err == nil {
			t.Fatal("headless restarted")
		}
		if b, _ := os.ReadFile(exe); string(b) != "old" {
			t.Fatal("headless replaced itself")
		}
	})
	t.Run("no release is an error that says why", func(t *testing.T) {
		updateSandbox(t, "0.1.2")
		srv := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(srv.Close)
		t.Setenv("LOOPBEE_UPDATE_FEED", srv.URL+"/latest")
		a := testApp(t)
		a.EnableUpdates(func() {})
		a.CheckUpdate()
		if s := waitUpdate(t, a, "error"); !strings.Contains(s.Error, "gh auth login") {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("quitting puts a downloaded update in", func(t *testing.T) {
		exe, restarts, _ := updateSandbox(t, "0.1.2")
		releaseFeed(t, "0.1.3", "new loopbee")
		a, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		a.EnableUpdates(func() { restarts.Add(1) })
		a.CheckUpdate()
		waitUpdate(t, a, "ready")
		a.Close()
		if b, _ := os.ReadFile(exe); string(b) != "new loopbee" || restarts.Load() != 0 {
			t.Fatalf("installed %q restarts %d", b, restarts.Load())
		}
	})
	t.Run("viewers cannot restart", func(t *testing.T) {
		updateSandbox(t, "0.1.2")
		a := testApp(t)
		c := a.Settings()
		c.Role = "viewer"
		a.SaveSettings(c)
		if _, err := a.RestartToUpdate("now"); err == nil {
			t.Fatal("viewer restarted")
		}
	})
}

// Settings accept every registered agent as the default one (the list used to name only Codex and Claude Code).
func TestDefaultAgentIsAnyAdapter(t *testing.T) {
	a := testApp(t)
	for _, name := range AgentNames() {
		c := a.Settings()
		c.DefaultAgent = name
		if err := a.SaveSettings(c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	c := a.Settings()
	c.DefaultAgent = "nope"
	if a.SaveSettings(c) == nil {
		t.Fatal("unknown agent accepted")
	}
}
