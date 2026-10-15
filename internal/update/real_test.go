package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The real release, end to end except the restart: read the newest release of Repo (with the GitHub CLI's token
// for a private repository), download this Mac's archive, check it against SHA256SUMS, unpack it and swap it in
// for a stand-in loopbee.app in a temporary folder. The installed app is never touched. Run on a Mac with
// LOOPBEE_REAL_UPDATE=1 (it downloads about 25 MB).
func TestRealReleaseStagesAndInstalls(t *testing.T) {
	if os.Getenv("LOOPBEE_REAL_UPDATE") != "1" || runtime.GOOS != "darwin" {
		t.Skip("set LOOPBEE_REAL_UPDATE=1 on a Mac to download the real release")
	}
	c := &Client{Token: func() string {
		out, err := exec.Command("gh", "auth", "token", "--hostname", "github.com").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	rel, err := c.Latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("newest release %s, %d files", rel.Version, len(rel.Assets))
	dir := t.TempDir()
	bundle := filepath.Join(dir, "loopbee.app")
	os.MkdirAll(filepath.Join(bundle, "Contents", "MacOS"), 0o755)
	os.WriteFile(filepath.Join(bundle, "Contents", "MacOS", "loopbee"), []byte("old"), 0o755)
	target := Locate("darwin", filepath.Join(bundle, "Contents", "MacOS", "loopbee"))
	if target.Stuck != "" {
		t.Fatalf("%+v", target)
	}
	staged, err := c.Stage(ctx, rel, target, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(staged, target, false); err != nil {
		t.Fatal(err)
	}
	plist, _ := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if !strings.Contains(string(plist), "<string>"+strings.SplitN(rel.Version, "-", 2)[0]+"</string>") {
		t.Fatalf("installed app is not %s", rel.Version)
	}
	if out, err := exec.Command("lipo", "-archs", filepath.Join(bundle, "Contents", "MacOS", "loopbee")).CombinedOutput(); err != nil {
		t.Fatalf("installed binary: %v %s", err, out)
	} else {
		t.Logf("installed %s (%s)", rel.Version, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(filepath.Join(dir, ".loopbee-update")); err == nil {
		t.Fatal("staging folder left")
	}
}
