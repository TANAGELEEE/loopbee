package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loopbee/internal/core"
	"loopbee/internal/httpapi"
)

func startApp(t *testing.T) (*core.App, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := core.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	l, err := httpapi.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	api := httpapi.New(a, l.Addr().String())
	srv := httpapi.HTTPServer(api.Handler())
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	url := "http://" + l.Addr().String() + "/#token=" + api.Token
	if err := os.WriteFile(filepath.Join(dir, "launch-url.txt"), []byte(url), 0600); err != nil {
		t.Fatal(err)
	}
	return a, dir
}

func TestBeectlCreatesDisabledDraftAndRuns(t *testing.T) {
	a, dir := startApp(t)
	work := t.TempDir()
	def := `{"name":"Nightly fix","directory":"` + filepath.ToSlash(work) + `","cron":"0 2 * * *","enabled":true,
	  "steps":[{"kind":"gate","name":"Review"}],"env":[{"name":"DB_PASSWORD","secret":true}]}`
	file := filepath.Join(t.TempDir(), "loop.json")
	os.WriteFile(file, []byte(def), 0600)
	var out bytes.Buffer
	if err := run([]string{"-data", dir, "create", "-f", file}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Created draft \"Nightly fix\"") {
		t.Fatal(out.String())
	}
	s, _ := a.State()
	if len(s.Loops) != 1 || s.Loops[0].Enabled || !s.Loops[0].Private || s.Loops[0].Source != "beectl" {
		t.Fatalf("draft must be disabled and private: %+v", s.Loops)
	}
	out.Reset()
	if err := run([]string{"-data", dir, "loops"}, &out); err != nil || !strings.Contains(out.String(), "Nightly fix") {
		t.Fatal(err, out.String())
	}
	// Secret without value blocks the run but the command itself works.
	out.Reset()
	if err := run([]string{"-data", dir, "run", "Nightly fix"}, &out); err != nil || !strings.Contains(out.String(), "Started run") {
		t.Fatal(err, out.String())
	}
	// A secret value in the definition is refused before it reaches the app.
	os.WriteFile(file, []byte(`{"name":"Leaky","directory":"`+filepath.ToSlash(work)+`","steps":[{"kind":"gate","name":"g"}],"env":[{"name":"TOKEN","secret":true,"value":"abc"}]}`), 0600)
	if err := run([]string{"-data", dir, "create", "-f", file}, &out); err == nil || !strings.Contains(err.Error(), "must not be in the definition") {
		t.Fatal(err)
	}
	// Unknown fields are rejected with a pointer to the schema.
	os.WriteFile(file, []byte(`{"name":"x","bogus":1}`), 0600)
	if err := run([]string{"-data", dir, "create", "-f", file}, &out); err == nil || !strings.Contains(err.Error(), "beectl schema") {
		t.Fatal(err)
	}
}

func TestBeectlWithoutAppAndSkillInstall(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-data", t.TempDir(), "status"}, &out); err == nil || !strings.Contains(err.Error(), "start the app first") {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := run([]string{"skill", "install", "--dir", target}, &out); err != nil {
		t.Fatal(err)
	}
	checkInstalledSkill(t, target)
	out.Reset()
	if err := run([]string{"schema"}, &out); err != nil || !strings.Contains(out.String(), `"sandbox"`) || !strings.Contains(out.String(), "Fix production errors") {
		t.Fatal(err, out.String())
	}
	_ = http.MethodGet
}

func checkInstalledSkill(t *testing.T, dir string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("skill not written: %v", err)
	}
	if strings.Contains(string(b), "\r") {
		t.Fatal("installed skill has CRLF line endings")
	}
	if head, _, _ := strings.Cut(string(b), "\n\n"); !strings.HasPrefix(string(b), "---\nname: loopbee") {
		t.Fatalf("installed skill has unexpected front matter: %q", head)
	}
}

// A Windows checkout with core.autocrlf=true embeds SKILL.md with CRLF; install and schema must not depend on it.
func TestSkillWithCRLFCheckout(t *testing.T) {
	orig := skillRaw
	t.Cleanup(func() { skillRaw = orig })
	skillRaw = []byte(strings.ReplaceAll(strings.ReplaceAll(string(orig), "\r\n", "\n"), "\n", "\r\n"))
	target := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"skill", "install", "--dir", target}, &out); err != nil {
		t.Fatal(err)
	}
	checkInstalledSkill(t, target)
	out.Reset()
	if err := run([]string{"schema"}, &out); err != nil || !strings.Contains(out.String(), "Fix production errors") {
		t.Fatalf("schema example lost with CRLF: %v %s", err, out.String())
	}
}
