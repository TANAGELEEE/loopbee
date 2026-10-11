package core

import (
	"archive/zip"
	"bytes"
	"os"
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
