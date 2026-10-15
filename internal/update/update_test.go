package update

import (
	"archive/zip"
	"bytes"
	"context"
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
	"testing"
)

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		newer bool
	}{{"0.1.2", "0.1.1", true}, {"v0.2.0", "0.1.9", true}, {"0.1.1", "0.1.1", false}, {"0.2.0", "0.2.0-rc.1", true},
		{"0.2.0-rc.2", "0.2.0-rc.10", false}, {"1.0.0", "garbage", false}} {
		if got := Newer(c.a, c.b); got != c.newer {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
	for v, want := range map[string]bool{"0.1.2": true, "0.2.0-rc.1": true, "0.1.0-dev": false, "0.1.0-3-g0bcb2cc": false, "0.1.0-dirty": false, "dev": false} {
		if Released(v) != want {
			t.Errorf("Released(%s) != %v", v, want)
		}
	}
	if got := ParseSums(strings.NewReader(strings.Repeat("a", 64) + "  loopbee-macos-arm64.zip\n" + strings.Repeat("B", 64) + " *x.exe\nbad line\n")); got["loopbee-macos-arm64.zip"] != strings.Repeat("a", 64) || got["x.exe"] != strings.Repeat("b", 64) || len(got) != 2 {
		t.Fatal(got)
	}
}

// feed serves a GitHub-style release with the given files and a SHA256SUMS listing sums (the files' own hashes
// unless overridden); private: anonymous requests get 404 and the token "t0k" is required, as for a private
// repository.
func feed(t *testing.T, version string, files map[string][]byte, sums map[string]string, private bool) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if private && r.Header.Get("Authorization") != "Bearer t0k" {
			http.NotFound(w, r)
			return
		}
		switch {
		case r.URL.Path == "/latest":
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
				API  string `json:"url"`
				Size int    `json:"size"`
			}
			list := []asset{{Name: "SHA256SUMS", URL: srv.URL + "/dl/SHA256SUMS", API: srv.URL + "/api/SHA256SUMS"}}
			for name, b := range files {
				list = append(list, asset{Name: name, URL: srv.URL + "/dl/" + name, API: srv.URL + "/api/" + name, Size: len(b)})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + version, "html_url": "https://example.invalid/r/v" + version, "body": "## Notes\nFaster.", "assets": list})
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			for name, b := range files {
				sum := sha256.Sum256(b)
				h := hex.EncodeToString(sum[:])
				if s, ok := sums[name]; ok {
					h = s
				}
				fmt.Fprintf(w, "%s  %s\n", h, name)
			}
		default:
			name := filepath.Base(r.URL.Path)
			if private && !strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			w.Write(files[name])
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LOOPBEE_UPDATE_FEED", srv.URL+"/latest")
	return srv
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, text := range files {
		w, _ := z.Create(name)
		w.Write([]byte(text))
	}
	z.Close()
	return b.Bytes()
}

// A release is accepted only with a published checksum that matches; a private repository is read with the
// GitHub CLI's token.
func TestLatestChecksumsAndPrivateRepository(t *testing.T) {
	name := "loopbee-windows-" + runtime.GOARCH + ".zip"
	archive := zipOf(t, map[string]string{"loopbee.exe": "new"})
	feed(t, "0.1.3", map[string][]byte{name: archive}, nil, true)
	if _, err := (&Client{}).Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("anonymous: %v", err)
	}
	c := &Client{Token: func() string { return "t0k" }}
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.1.3" || !strings.Contains(rel.Notes, "Faster") {
		t.Fatalf("%+v", rel)
	}
	a, _ := rel.asset(name)
	sum := sha256.Sum256(archive)
	if a.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("checksum not read: %+v", a)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := c.Fetch(context.Background(), a, dst); err != nil {
		t.Fatal(err)
	}
	a.SHA256 = strings.Repeat("0", 64)
	if err := c.Fetch(context.Background(), a, dst); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a wrong checksum: %v", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("a file that failed its checksum is kept")
	}
	a.SHA256 = ""
	if err := c.Fetch(context.Background(), a, dst); err == nil || !strings.Contains(err.Error(), "no checksum") {
		t.Fatalf("no checksum: %v", err)
	}
}

// On Windows the running program is moved aside and the new one put in its place under the same name; beectl and
// loopbee-nogui are replaced only where they are installed; paths inside the archive are never used.
func TestStageAndInstallWindows(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "loopbee-windows-amd64.exe")
	os.WriteFile(exe, []byte("old"), 0o755)
	os.WriteFile(filepath.Join(dir, "beectl.exe"), []byte("old beectl"), 0o755)
	name := "loopbee-windows-" + runtime.GOARCH + ".zip"
	archive := zipOf(t, map[string]string{"loopbee.exe": "new", "beectl.exe": "new beectl", "loopbee-nogui.exe": "new nogui", "../evil.exe": "x", "sub/loopbee.exe": "x"})
	feed(t, "0.1.3", map[string][]byte{name: archive}, nil, false)
	c := &Client{}
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	target := Locate("windows", exe)
	if target.Stuck != "" || target.AssetName(runtime.GOARCH) != name {
		t.Fatalf("%+v", target)
	}
	var progress int64
	c.Progress = func(done, total int64) { progress = done }
	staged, err := c.Stage(context.Background(), rel, target, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if progress != int64(len(archive)) {
		t.Fatalf("progress %d of %d", progress, len(archive))
	}
	old, err := Install(staged, target, false)
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string { b, _ := os.ReadFile(filepath.Join(dir, name)); return string(b) }
	if read("loopbee-windows-amd64.exe") != "new" || read("beectl.exe") != "new beectl" || old != exe+".old" || read("loopbee-windows-amd64.exe.old") != "old" {
		t.Fatalf("exe %q beectl %q old %q", read("loopbee-windows-amd64.exe"), read("beectl.exe"), old)
	}
	if _, err := os.Stat(filepath.Join(dir, "loopbee-nogui.exe")); err == nil {
		t.Fatal("a program that was not installed was added")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.exe")); err == nil {
		t.Fatal("a path from the archive was used")
	}
	if _, err := os.Stat(filepath.Join(dir, ".loopbee-update")); err == nil {
		t.Fatal("the staging folder was left")
	}
	RemoveOld(exe)
	if _, err := os.Stat(old); err == nil {
		t.Fatal("the old program was not removed")
	}
	// A tampered archive (checksum mismatch) is never unpacked.
	sum := strings.Repeat("1", 64)
	feed(t, "0.1.4", map[string][]byte{name: archive}, map[string]string{name: sum}, false)
	rel, _ = c.Latest(context.Background())
	if _, err := c.Stage(context.Background(), rel, target, runtime.GOARCH); err == nil {
		t.Fatal("a tampered archive was staged")
	}
}

func TestLocate(t *testing.T) {
	for exe, want := range map[string]string{
		"/Applications/loopbee.app/Contents/MacOS/loopbee":                               "",
		"/private/var/folders/x/AppTranslocation/1/d/loopbee.app/Contents/MacOS/loopbee": "translocated",
		"/Users/me/bin/loopbee": "unsupported",
	} {
		if got := Locate("darwin", exe).Stuck; got != want {
			t.Errorf("%s: %q", exe, got)
		}
	}
	if Locate("linux", "/usr/bin/loopbee").Stuck != "unsupported" {
		t.Fatal("linux has no release build")
	}
	if got := (Target{OS: "darwin"}).AssetName("arm64"); got != "loopbee-macos-arm64.zip" {
		t.Fatal(got)
	}
	if got := swapScript("/a b/new.app", "/Applications/loopbee.app", "/x/old'.app"); !strings.Contains(got, `'/a b/new.app'`) || !strings.Contains(got, `'/x/old'\''.app'`) {
		t.Fatal(got)
	}
}

// A bundle is swapped in with a rename and put back when the second rename fails.
func TestSwapBundle(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "loopbee.app")
	os.MkdirAll(filepath.Join(bundle, "Contents"), 0o755)
	os.WriteFile(filepath.Join(bundle, "Contents", "v"), []byte("old"), 0o644)
	staged := filepath.Join(dir, ".loopbee-update", "new", "loopbee.app")
	os.MkdirAll(filepath.Join(staged, "Contents"), 0o755)
	os.WriteFile(filepath.Join(staged, "Contents", "v"), []byte("new"), 0o644)
	if _, err := Install(staged, Target{OS: "darwin", Bundle: bundle}, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(bundle, "Contents", "v")); string(b) != "new" {
		t.Fatalf("%q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, ".loopbee-update")); err == nil {
		t.Fatal("staging folder left")
	}
	if err := swapBundle(filepath.Join(dir, "missing.app"), bundle); err == nil {
		t.Fatal("swap of a missing app")
	}
	if b, _ := os.ReadFile(filepath.Join(bundle, "Contents", "v")); string(b) != "new" {
		t.Fatal("the installed app was not put back")
	}
}
