package core

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestArchiveRoundTrip(t *testing.T) {
	t.Setenv("LOOPBEE_HELPER", "success")
	a := testApp(t)
	l := testLoop(t)
	l.AllowedTools = []string{os.Args[0]}
	l.Steps = []Step{{Name: "Evidence", Kind: "verify", Argv: []string{os.Args[0], "-test.run=TestHelperProcess"}}}
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Start(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, a, r.ID, "completed")
	var b bytes.Buffer
	if err = a.Export(&b); err != nil {
		t.Fatal(err)
	}
	other := testApp(t)
	if err = other.ImportArchive(b.Bytes()); err != nil {
		t.Fatal(err)
	}
	state, err := other.State()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Loops) != 1 || state.Loops[0].Enabled || !state.Loops[0].Private || state.Loops[0].ID == l.ID {
		t.Fatal("import did not create a private disabled copy")
	}
	runs := state.Runs
	if len(runs) != 1 || runs[0].ID == r.ID || len(runs[0].Artifacts) != 1 {
		t.Fatal("run not restored")
	}
	path, err := other.ArtifactPath(runs[0].ID, runs[0].Artifacts[0])
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("evidence")) {
		t.Fatal("artifact corrupted")
	}
	events, err := other.Events(runs[0].ID)
	if err != nil || len(events) == 0 {
		t.Fatal("events not restored")
	}
}
func TestArchiveTraversalRejected(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("loopbee/manifest.json")
	f.Write([]byte(`{"version":1,"loops":[],"runs":[]}`))
	f, _ = z.Create("../../secret")
	f.Write([]byte("bad"))
	z.Close()
	a := testApp(t)
	if err := a.ImportArchive(b.Bytes()); err == nil {
		t.Fatal("unexpected archive path accepted")
	}
}
func TestDefinitionYAML(t *testing.T) {
	l, err := ParseDefinition("name: Example\nenabled: true\nsteps:\n  - kind: gate\n    name: Review\n")
	if err != nil {
		t.Fatal(err)
	}
	if l.Enabled || !l.Private || l.Name != "Example" {
		t.Fatal("unsafe import defaults")
	}
	if _, err = ParseDefinition("unknown: true\nsteps: []"); err == nil {
		t.Fatal("unknown definition accepted")
	}
}
func TestEnvironmentCredentials(t *testing.T) {
	t.Setenv("LOOPBEE_TEST_SECRET", "secret-value")
	got, err := resolveSecret("env:LOOPBEE_TEST_SECRET")
	if err != nil || got != "secret-value" {
		t.Fatal("environment reference not resolved")
	}
	if _, err = resolveSecret("env:LOOPBEE_MISSING_SECRET"); err == nil {
		t.Fatal("missing credential accepted")
	}
}

// Older loopbee reads archives leniently and does not validate imported Loops: it would drop a review loop's
// fields, or a step's lower permission, without a word. Archives that use them get a format older versions refuse.
func TestArchiveFormatFollowsWhatOlderVersionsWouldDrop(t *testing.T) {
	plain := Loop{Steps: []Step{{ID: "fix", Kind: "act", Prompt: "fix"}, {ID: "review", Kind: "gate"}}}
	if v := archiveFormat([]Loop{plain}, nil); v != 1 {
		t.Fatalf("a Loop without new fields must stay readable by older versions, got format %d", v)
	}
	for name, change := range map[string]func(*Loop){
		"review step":  func(l *Loop) { l.Steps[1] = Step{ID: "review", Kind: "review", Prompt: "check", SendBackTo: "fix"} },
		"step sandbox": func(l *Loop) { l.Steps[0].Sandbox = "read-only" },
		"step agent":   func(l *Loop) { l.Steps[0].Agent = "claude" },
		"step inputs":  func(l *Loop) { l.Steps[0].Inputs = []string{"x"} },
		"step effort":  func(l *Loop) { l.Steps[0].Effort = "low" },
		"step model":   func(l *Loop) { l.Steps[0].Model = "sonnet" },
		"loop effort":  func(l *Loop) { l.Effort = "low" },
		"deliver step": func(l *Loop) {
			l.Steps[1] = Step{ID: "review", Kind: "deliver", PullRequest: &PullRequest{Mode: "draft"}}
		},
	} {
		l := plain
		l.Steps = append([]Step(nil), plain.Steps...)
		change(&l)
		if v := archiveFormat([]Loop{l}, nil); v != 2 {
			t.Errorf("%s: format %d, want 2", name, v)
		}
		if v := archiveFormat(nil, []Run{{Definition: l}}); v != 2 {
			t.Errorf("%s in a Run's definition: format %d, want 2", name, v)
		}
	}
	a := testApp(t)
	for _, v := range []int{1, 2} {
		if err := a.ImportArchive(testArchive(t, v)); err != nil {
			t.Fatalf("format %d: %v", v, err)
		}
	}
	err := a.ImportArchive(testArchive(t, 3))
	if err == nil || !strings.Contains(err.Error(), "newer loopbee") || !strings.Contains(err.Error(), "update") {
		t.Fatalf("a newer format must say to update loopbee, got %v", err)
	}
}

func testArchive(t *testing.T, version int) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, err := z.Create("loopbee/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(w).Encode(Archive{Version: version, Loops: []Loop{}, Runs: []Run{}, Events: map[string][]Event{}}); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
