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
	"loopbee/internal/testenv"
)

func startApp(t *testing.T) (*core.App, string) {
	t.Helper()
	testenv.Loopback(t)
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
	// A draft above the device limit says which level it needs and where to raise the limit.
	os.WriteFile(file, []byte(`{"name":"Writer","directory":"`+filepath.ToSlash(work)+`","sandbox":"workspace-write","steps":[{"kind":"act","name":"a","prompt":"p"}]}`), 0600)
	if err := run([]string{"-data", dir, "create", "-f", file}, &out); err == nil || !strings.Contains(err.Error(), `it needs the permission "workspace-write", but this device allows at most "read-only"`) || !strings.Contains(err.Error(), "Settings → General → Maximum permission") {
		t.Fatal(err)
	}
	// Without a directory in the definition, --dir (or the current folder) is the project.
	os.WriteFile(file, []byte(`{"name":"No dir","steps":[{"kind":"gate","name":"g"}]}`), 0600)
	if err := run([]string{"-data", dir, "create", "-f", file, "--dir", work}, &out); err != nil {
		t.Fatal(err)
	}
	s, _ = a.State()
	for _, l := range s.Loops {
		if l.Name == "No dir" && l.Directory != work {
			t.Fatalf("directory %q, want %q", l.Directory, work)
		}
	}
	// Unknown fields are rejected with a pointer to the schema.
	os.WriteFile(file, []byte(`{"name":"x","bogus":1}`), 0600)
	if err := run([]string{"-data", dir, "create", "-f", file}, &out); err == nil || !strings.Contains(err.Error(), "beectl schema") {
		t.Fatal(err)
	}
}

func TestBeectlVersionWithoutApp(t *testing.T) {
	t.Setenv("LOOPBEE_DATA_DIR", t.TempDir())
	for _, args := range [][]string{{"version"}, {"--version"}, {"-data", t.TempDir(), "version"}, {"-data", t.TempDir(), "--version"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out bytes.Buffer
			if err := run(args, &out); err != nil {
				t.Fatal(err)
			}
			if want := "beectl " + core.Version + "\n"; out.String() != want {
				t.Fatalf("got %q, want %q", out.String(), want)
			}
		})
	}
}

func TestBeectlHelpListsVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"help"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "version") || !strings.Contains(out.String(), "--version") {
		t.Fatalf("help omits version command or flag: %s", out.String())
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

// Every example definition must load through beectl and pass validation.
func TestExampleLoopsAreValid(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	files, _ := filepath.Glob(filepath.Join(root, "examples", "*.json"))
	if len(files) < 2 {
		t.Fatalf("examples: %v", files)
	}
	for _, f := range files {
		src, _ := os.ReadFile(f)
		l, err := draftFromJSON(src, core.Settings{DefaultAgent: "codex", DigestTimezone: "UTC"})
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
		l.Directory = root
		if err := core.Validate(l); err != nil {
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
		// No command may run after an agent step before a gate: it would run the agent's changes unreviewed.
		agent, gate := false, false
		for _, s := range l.Steps {
			switch s.Kind {
			case "gate":
				gate = true
			case "act", "observe", "summarize":
				agent = true
			case "run", "verify":
				if agent && !gate {
					t.Fatalf("%s: %q runs after the agent without review", filepath.Base(f), s.Name)
				}
			}
		}
	}
}

// The self-improvement Loop is what docs/workflows/issue-loop.md tells people to create; keep it valid.
func TestSelfImproveExampleIsValid(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	src, err := os.ReadFile(filepath.Join(root, "examples", "self-improve.json"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := draftFromJSON(src, core.Settings{DefaultAgent: "codex", DigestTimezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	l.Directory = root
	if err := core.Validate(l); err != nil {
		t.Fatal(err)
	}
	if l.Enabled || l.Intake == nil || l.PullRequest == nil || l.PullRequest.Mode != "after-review" || l.Branch != "loopbee/issue-{issue}" || len(l.AllowedTools) != 0 {
		t.Fatalf("unexpected definition: %+v", l)
	}
	// Picking work and opening the pull request are Loopbee's job: no project scripts, no commands at all.
	for _, s := range l.Steps {
		if s.Kind == "run" || s.Kind == "verify" {
			t.Fatalf("step %q: the example must not run commands", s.Name)
		}
	}
}
