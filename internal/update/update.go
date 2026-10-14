// Package update keeps Loopbee current, after magpie's updater (github.com/yetone/magpie, internal/update; MIT).
//
// Releases are GitHub releases of Repo. The newest one is read from GitHub's API; its files are checked against
// the release's SHA256SUMS (else the digest GitHub lists per file) before anything is unpacked. The macOS app
// replaces its own bundle; on Windows the desktop program replaces its .exe (and beectl / loopbee-nogui beside
// it) by moving the running file aside first. Builds from source never update themselves.
package update

import (
	"archive/zip"
	"bufio"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is the GitHub repository whose releases are Loopbee's (owner/name). Release builds set it with
// -ldflags "-X loopbee/internal/update.Repo=…"; LOOPBEE_UPDATE_REPO overrides it.
var Repo = "TANAGELEEE/Loopbee"

// Feed is the URL of the newest release in GitHub's API format. LOOPBEE_UPDATE_FEED points it elsewhere, for
// testing an update against a local server.
func Feed() string {
	if f := os.Getenv("LOOPBEE_UPDATE_FEED"); f != "" {
		return f
	}
	repo := Repo
	if r := os.Getenv("LOOPBEE_UPDATE_REPO"); r != "" {
		repo = r
	}
	return "https://api.github.com/repos/" + repo + "/releases/latest"
}

// Release is one published version.
type Release struct {
	Version string  // "0.1.2", no v
	Notes   string  // markdown
	URL     string  // the release page
	Assets  []Asset // its files
}

// Asset is one downloadable file of a release.
type Asset struct {
	Name   string
	URL    string // browser download URL
	API    string // API URL (authenticated downloads of a private repository)
	Size   int64
	SHA256 string // from SHA256SUMS or GitHub's digest; "" when unknown
}

func (r *Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Client downloads through the environment's proxy; Token, when set, signs requests to GitHub's API (a private
// repository: the GitHub CLI's token). The token is only ever sent to the API's host (or the test feed's).
type Client struct {
	HTTP  *http.Client
	Token func() string
	// Progress, when set, is told how much of a download has arrived (total 0 while unknown).
	Progress func(done, total int64)

	once  sync.Once
	token string
}

func (c *Client) bearer(rawURL string) string {
	if c == nil || c.Token == nil {
		return ""
	}
	u, err := url.Parse(rawURL)
	feed, ferr := url.Parse(Feed())
	if err != nil || ferr != nil || u.Scheme != "https" && u.Host != feed.Host || (u.Host != "api.github.com" && u.Host != feed.Host) {
		return ""
	}
	c.once.Do(func() { c.token = c.Token() })
	return c.token
}

func (c *Client) http() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

// Latest reads the newest release and its checksums.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	var raw struct {
		Tag    string `json:"tag_name"`
		Body   string `json:"body"`
		URL    string `json:"html_url"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			API    string `json:"url"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	res, err := c.get(ctx, Feed(), "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("update feed: %w", err)
	}
	if parse(raw.Tag) == nil || raw.Draft {
		return nil, errors.New("update feed: no released version")
	}
	r := &Release{Version: strings.TrimPrefix(raw.Tag, "v"), Notes: raw.Body, URL: raw.URL}
	for _, a := range raw.Assets {
		sum := ""
		if d, ok := strings.CutPrefix(a.Digest, "sha256:"); ok {
			sum = strings.ToLower(d)
		}
		r.Assets = append(r.Assets, Asset{Name: a.Name, URL: a.URL, API: a.API, Size: a.Size, SHA256: sum})
	}
	// SHA256SUMS, written by the release workflow, is the authority; GitHub's digest only fills in.
	if sums, ok := r.asset("SHA256SUMS"); ok {
		res, err := c.download(ctx, sums)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		listed := ParseSums(io.LimitReader(res.Body, 1<<20))
		for i, a := range r.Assets {
			if s, ok := listed[a.Name]; ok {
				r.Assets[i].SHA256 = s
			}
		}
	}
	return r, nil
}

// get asks url, signed with the token when GitHub refuses an anonymous request (a private repository).
func (c *Client) get(ctx context.Context, rawURL, accept string) (*http.Response, error) {
	url := rawURL
	do := func(token string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", "Loopbee-updater")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return c.http().Do(req)
	}
	res, err := do("")
	if err != nil {
		return nil, err
	}
	if res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 404 {
		if token := c.bearer(url); token != "" {
			res.Body.Close()
			if res, err = do(token); err != nil {
				return nil, err
			}
		}
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		if res.StatusCode == 404 {
			return nil, fmt.Errorf("%s: %s (no release yet, or a private repository: sign in with gh auth login)", url, res.Status)
		}
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	return res, nil
}

// download opens an asset: through the API with the token when there is one (private repositories), else its
// download URL.
func (c *Client) download(ctx context.Context, a Asset) (*http.Response, error) {
	if a.API != "" {
		if token := c.bearer(a.API); token != "" {
			req, err := http.NewRequestWithContext(ctx, "GET", a.API, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Accept", "application/octet-stream")
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("User-Agent", "Loopbee-updater")
			// Go drops the Authorization header on the redirect to the storage host.
			if res, err := c.http().Do(req); err == nil && res.StatusCode == 200 {
				return res, nil
			} else if err == nil {
				res.Body.Close()
			}
		}
	}
	return c.get(ctx, a.URL, "application/octet-stream")
}

// Fetch downloads a to path and checks its SHA-256. A dropped connection gets two more tries.
func (c *Client) Fetch(ctx context.Context, a Asset, path string) error {
	if a.SHA256 == "" {
		return fmt.Errorf("the release lists no checksum for %s; not installed", a.Name)
	}
	var err error
	for try := 0; try < 3; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return err
			case <-time.After(time.Duration(try) * 2 * time.Second):
			}
		}
		if err = c.fetch(ctx, a, path); err == nil || ctx.Err() != nil || errors.Is(err, errChecksum) {
			break
		}
	}
	return err
}

var errChecksum = errors.New("checksum mismatch")

func (c *Client) fetch(ctx context.Context, a Asset, path string) error {
	res, err := c.download(ctx, a)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	var body io.Reader = res.Body
	if c != nil && c.Progress != nil {
		total := res.ContentLength
		if total <= 0 {
			total = a.Size
		}
		body = &counter{r: res.Body, total: max(total, 0), report: c.Progress}
		c.Progress(0, max(total, 0))
	}
	_, err = io.Copy(io.MultiWriter(f, h), body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.SHA256) {
		err = fmt.Errorf("%s does not match its published checksum; not installed: %w", a.Name, errChecksum)
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

type counter struct {
	r      io.Reader
	done   int64
	total  int64
	report func(done, total int64)
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	c.report(c.done, c.total)
	return n, err
}

// ParseSums reads a SHA256SUMS file ("<hex>  <name>" per line, "*name" in binary mode).
func ParseSums(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && len(f[0]) == 64 {
			out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	return out
}

// Released reports whether v is a release version rather than a build from source ("0.1.0-dev", git describe's
// "0.1.0-3-g0bcb2cc", "-dirty"); only releases update themselves.
func Released(v string) bool {
	s := parse(v)
	if s == nil {
		return false
	}
	for _, p := range strings.Split(s.pre, ".") {
		if p == "dev" || strings.Contains(p, "dirty") || (strings.HasPrefix(p, "g") && len(p) > 6 && isHex(p[1:])) {
			return false
		}
	}
	return !strings.Contains(s.pre, "-g")
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s + strings.Repeat("0", len(s)%2))
	return err == nil
}

// Newer reports whether version a comes after b (semver: a pre-release comes before its release).
func Newer(a, b string) bool {
	x, y := parse(a), parse(b)
	if x == nil || y == nil {
		return false
	}
	for i := range 3 {
		if x.n[i] != y.n[i] {
			return x.n[i] > y.n[i]
		}
	}
	return comparePre(x.pre, y.pre) > 0
}

func comparePre(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(x) && i < len(y); i++ {
		m, errM := strconv.ParseUint(x[i], 10, 64)
		n, errN := strconv.ParseUint(y[i], 10, 64)
		switch {
		case errM == nil && errN == nil:
			if m != n {
				return cmp.Compare(m, n)
			}
		case errM == nil:
			return -1
		case errN == nil:
			return 1
		default:
			if c := strings.Compare(x[i], y[i]); c != 0 {
				return c
			}
		}
	}
	return cmp.Compare(len(x), len(y))
}

type semver struct {
	n   [3]int
	pre string
}

func parse(v string) *semver {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	v, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}
	var s semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		s.n[i] = n
	}
	s.pre = pre
	return &s
}

// Target is what an update replaces on this computer.
type Target struct {
	OS     string // runtime.GOOS
	Bundle string // macOS: the Loopbee.app the program runs from
	Exe    string // Windows: the running desktop program
	// Stuck says why it cannot replace itself where it is ("" when it can): "translocated" (macOS moved it aside
	// because it never left Downloads), "read-only" (run from the disk image), "not-writable" (a folder it may
	// not write to and no administrator prompt), "unsupported" (no release build for this system).
	Stuck string
}

// Locate finds what an update replaces for the running program (exe, symlinks resolved).
func Locate(goos, exe string) Target {
	t := Target{OS: goos}
	switch goos {
	case "darwin":
		app := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // …/Loopbee.app/Contents/MacOS/loopbee
		if filepath.Ext(app) != ".app" || filepath.Base(filepath.Dir(exe)) != "MacOS" {
			t.Stuck = "unsupported"
			return t
		}
		t.Bundle = app
		switch {
		case strings.Contains(app, "/AppTranslocation/"):
			t.Stuck = "translocated"
		case strings.HasPrefix(app, "/Volumes/") && !Writable(filepath.Dir(app)):
			t.Stuck = "read-only"
		}
	case "windows":
		t.Exe = exe
		if !Writable(filepath.Dir(exe)) {
			t.Stuck = "not-writable"
		}
	default:
		t.Stuck = "unsupported"
	}
	return t
}

// AssetName is the release file this target installs from.
func (t Target) AssetName(arch string) string {
	if t.OS == "darwin" {
		return "Loopbee-macos-" + arch + ".zip"
	}
	return "Loopbee-" + t.OS + "-" + arch + ".zip"
}

// Writable reports whether files can be created in dir.
func Writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".loopbee-update-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// stageDir is where an update is unpacked: beside what it replaces, so installing it is a rename on one volume,
// or in the cache when that folder is not Loopbee's to write (the administrator moves it in).
func stageDir(dir string) string {
	if !Writable(dir) {
		if cache, err := os.UserCacheDir(); err == nil {
			return filepath.Join(cache, "loopbee-update")
		}
	}
	return filepath.Join(dir, ".loopbee-update")
}

// Stage downloads the release's file for t, checks it and unpacks it; it returns what Install puts in place.
func (c *Client) Stage(ctx context.Context, rel *Release, t Target, arch string) (string, error) {
	if t.Stuck != "" {
		return "", fmt.Errorf("this copy of Loopbee cannot replace itself (%s)", t.Stuck)
	}
	name := t.AssetName(arch)
	a, ok := rel.asset(name)
	if !ok {
		return "", fmt.Errorf("release %s has no %s", rel.Version, name)
	}
	place := t.Bundle
	if place == "" {
		place = t.Exe
	}
	dir := stageDir(filepath.Dir(place))
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	archive := filepath.Join(dir, name)
	if err := c.Fetch(ctx, a, archive); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	out := filepath.Join(dir, "new")
	var err error
	if t.OS == "darwin" {
		err = unzipApp(ctx, archive, out)
	} else {
		err = unzipFiles(archive, out, []string{"loopbee.exe", "loopbee-nogui.exe", "beectl.exe"})
	}
	os.Remove(archive)
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if t.OS == "darwin" {
		app := filepath.Join(out, "Loopbee.app")
		if err := checkApp(ctx, t.Bundle, app, rel.Version); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		return app, nil
	}
	return out, nil
}

// unzipApp unpacks a macOS zip with ditto, which keeps the bundle's attributes and signature.
func unzipApp(ctx context.Context, archive, out string) error {
	if b, err := exec.CommandContext(ctx, "ditto", "-x", "-k", archive, out).CombinedOutput(); err != nil {
		return fmt.Errorf("unzip: %v: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// checkApp accepts the unpacked app only if it is Loopbee at the release's version and, when the installed app
// is signed by a team, signed intact by the same team.
func checkApp(ctx context.Context, installed, app, version string) error {
	if _, err := os.Stat(filepath.Join(app, "Contents", "MacOS", "loopbee")); err != nil {
		return errors.New("the downloaded archive has no Loopbee.app")
	}
	plist, _ := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if want := strings.SplitN(version, "-", 2)[0]; !strings.Contains(string(plist), "<string>"+want+"</string>") {
		return fmt.Errorf("the downloaded Loopbee.app is not version %s", version)
	}
	if want := team(ctx, installed); want != "" {
		if b, err := exec.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", app).CombinedOutput(); err != nil {
			return fmt.Errorf("the downloaded app's signature is broken: %s", strings.TrimSpace(string(b)))
		}
		if got := team(ctx, app); got != want {
			return fmt.Errorf("the downloaded app is signed by %q, not %q", got, want)
		}
	}
	return nil
}

func team(ctx context.Context, app string) string {
	b, _ := exec.CommandContext(ctx, "codesign", "-dv", app).CombinedOutput()
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "TeamIdentifier="); ok && v != "not set" {
			return v
		}
	}
	return ""
}

// unzipFiles extracts the named top-level files of a zip into out (names it lacks are skipped; none is an
// error). Paths are never taken from the archive.
func unzipFiles(archive, out string, names []string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	found := 0
	for _, f := range r.File {
		name := filepath.Base(filepath.FromSlash(f.Name))
		if f.FileInfo().IsDir() || name != f.Name || !containsFold(names, name) {
			continue
		}
		if f.UncompressedSize64 > 512<<20 {
			return fmt.Errorf("%s is too large", name)
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(filepath.Join(out, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err == nil {
			_, err = io.Copy(dst, io.LimitReader(src, 512<<20))
			if cerr := dst.Close(); err == nil {
				err = cerr
			}
		}
		src.Close()
		if err != nil {
			return err
		}
		found++
	}
	if found == 0 {
		return errors.New("the downloaded archive has none of Loopbee's programs")
	}
	return nil
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// Install puts the staged update in place of t. The running program keeps going until it quits; the next launch
// is the new one. It returns where the running program was moved aside (Windows), "" elsewhere. A folder the
// user may not write to on macOS is changed after the administrator's password (when ask).
func Install(staged string, t Target, ask bool) (string, error) {
	if t.OS == "darwin" {
		err := swapBundle(staged, t.Bundle)
		if err != nil && ask && needsAdmin(err) {
			err = asAdmin(swapScript(staged, t.Bundle, filepath.Join(filepath.Dir(staged), "old.app")))
		}
		if err == nil {
			os.RemoveAll(filepath.Dir(filepath.Dir(staged))) // .loopbee-update
		}
		return "", err
	}
	return installFiles(staged, t.Exe)
}

func swapBundle(staged, bundle string) error {
	old := filepath.Join(filepath.Dir(staged), "old.app")
	os.RemoveAll(old)
	if err := os.Rename(bundle, old); err != nil {
		return err
	}
	if err := os.Rename(staged, bundle); err != nil {
		os.Rename(old, bundle) // put it back
		return err
	}
	return nil
}

// installFiles puts the staged programs in place: loopbee.exe as the running program (whatever its name, such
// as Loopbee-windows-amd64.exe), beectl.exe and loopbee-nogui.exe only where they are already installed beside it.
// A running .exe cannot be overwritten, but it can be moved aside; the moved copy is removed at the next start.
func installFiles(staged, exe string) (string, error) {
	dir := filepath.Dir(exe)
	main := filepath.Join(staged, "loopbee.exe")
	if _, err := os.Stat(main); err != nil {
		return "", errors.New("the update has no loopbee.exe")
	}
	RemoveOld(exe)
	old, err := moveAside(exe)
	if err != nil {
		return "", err
	}
	if err := os.Rename(main, exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return "", errors.Join(err, fmt.Errorf("couldn't put %s back from %s: %w", filepath.Base(exe), old, rerr))
		}
		return "", err
	}
	for _, name := range []string{"beectl.exe", "loopbee-nogui.exe"} {
		dst := filepath.Join(dir, name)
		if _, err := os.Stat(dst); err != nil || strings.EqualFold(dst, exe) {
			continue
		}
		RemoveOld(dst)
		if aside, err := moveAside(dst); err == nil {
			if err := os.Rename(filepath.Join(staged, name), dst); err != nil {
				os.Rename(aside, dst)
			}
		}
	}
	os.RemoveAll(filepath.Dir(staged)) // .loopbee-update
	return old, nil
}

// moveAside renames a program out of the way under a name of its own, trying again for a few seconds while an
// antivirus or a sync client holds it open.
func moveAside(exe string) (string, error) {
	var err error
	for i, wait := range []time.Duration{0, 100 * time.Millisecond, 300 * time.Millisecond, 800 * time.Millisecond, 1500 * time.Millisecond} {
		time.Sleep(wait)
		old := oldName(exe)
		if err = os.Rename(exe, old); err == nil {
			return old, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if i == 0 {
			os.Chmod(exe, 0o755)
		}
	}
	return "", fmt.Errorf("couldn't move %s aside to put the new version in (%v); another program may have it open (often an antivirus or OneDrive): let Loopbee through it, or download the new version and replace this one", filepath.Base(exe), err)
}

func oldName(exe string) string {
	name := exe + ".old"
	for n := 2; ; n++ {
		if _, err := os.Lstat(name); os.IsNotExist(err) {
			return name
		}
		name = fmt.Sprintf("%s.old-%d", exe, n)
	}
}

// RemoveOld removes what earlier updates moved aside of exe, those no longer running.
func RemoveOld(exe string) {
	os.Remove(exe + ".old")
	olds, _ := filepath.Glob(exe + ".old-*")
	for _, o := range olds {
		os.Remove(o)
	}
}

func needsAdmin(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, errCrossDevice)
}

// ErrCanceled is the administrator's password prompt dismissed.
var ErrCanceled = errors.New("the update was canceled")

// asAdmin runs a shell script as root after macOS's own password prompt.
var asAdmin = func(script string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("cannot ask for the administrator's password here")
	}
	out, err := exec.Command("osascript", "-e", `do shell script "`+appleString(script)+`" with prompt "Loopbee is updating itself." with administrator privileges`).CombinedOutput()
	if err != nil && strings.Contains(string(out), "(-128)") {
		return ErrCanceled
	}
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
	}
	return err
}

// swapScript moves bundle aside to old and staged into its place, putting bundle back if the second move fails.
func swapScript(staged, bundle, old string) string {
	s, b, o := shellQuote(staged), shellQuote(bundle), shellQuote(old)
	return "rm -rf " + o + " && mv " + b + " " + o + " && if mv " + s + " " + b + "; then rm -rf " + o + "; else mv " + o + " " + b + "; exit 1; fi"
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func appleString(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// Relaunch starts the updated program once this process has exited: on macOS `open` of the bundle, after this
// pid is gone; on Windows the new .exe, told this pid (LOOPBEE_REPLACES) so it waits for the data folder's lock.
func Relaunch(t Target) error {
	switch t.OS {
	case "darwin":
		script := fmt.Sprintf(`while kill -0 %d 2>/dev/null; do sleep 0.2; done; open %s`, os.Getpid(), shellQuote(t.Bundle))
		cmd := exec.Command("/bin/sh", "-c", script)
		detach(cmd)
		return cmd.Start()
	case "windows":
		cmd := exec.Command(t.Exe)
		cmd.Env = append(os.Environ(), "LOOPBEE_REPLACES="+strconv.Itoa(os.Getpid()))
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	return errors.New("cannot restart here")
}

// AwaitPredecessor waits, for 30 seconds at most, until the Loopbee that relaunched this one has exited and let
// go of the data folder.
func AwaitPredecessor() {
	pid, err := strconv.Atoi(os.Getenv("LOOPBEE_REPLACES"))
	os.Unsetenv("LOOPBEE_REPLACES")
	if err != nil || pid <= 0 {
		return
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && alive(pid); {
		time.Sleep(100 * time.Millisecond)
	}
}
