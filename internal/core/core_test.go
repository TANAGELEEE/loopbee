package core

import (
	"context"
	"os"
	"testing"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func testLoop(t *testing.T) Loop {
	return Loop{Name: "Test", Directory: t.TempDir(), Agent: "codex", Sandbox: "read-only", Timezone: "UTC", MaxRounds: 1, WallMinutes: 1, StallRounds: 1, Steps: []Step{{Kind: "gate", Name: "Review"}}}
}

func TestValidation(t *testing.T) {
	l := testLoop(t)
	if err := Validate(l); err != nil {
		t.Fatal(err)
	}
	l.WallMinutes = 0
	if Validate(l) == nil {
		t.Fatal("unbounded runs must be rejected")
	}
	l.WallMinutes = 1
	l.Cron = "invalid"
	if Validate(l) == nil {
		t.Fatal("invalid cron accepted")
	}
	l.Cron = ""
	l.Sandbox = "danger-full-access"
	if Validate(l) == nil {
		t.Fatal("unsafe permission accepted")
	}
}

func TestGateSnapshotAndLock(t *testing.T) {
	a := testApp(t)
	l, err := a.SaveLoop(testLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Start(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, a, r.ID, "waiting")
	if _, err = a.Start(l.ID); err == nil {
		t.Fatal("duplicate run accepted")
	}
	l.Name = "New version"
	if _, err = a.SaveLoop(l); err != nil {
		t.Fatal(err)
	}
	old, _ := a.Run(r.ID)
	if old.Definition.Name != "Test" {
		t.Fatal("snapshot mutated")
	}
	if err = a.Decide(r.ID, true, "Reviewed"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, a, r.ID, "completed")
	if err = a.Decide(r.ID, true, "Again"); err == nil {
		t.Fatal("gate replay accepted")
	}
}

func TestRecovery(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := Run{ID: "crashed", Status: "running", Started: time.Now(), Definition: testLoop(t)}
	if err = a.saveRun(r); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	got, err := a.Run(r.ID)
	if err != nil || got.Status != "interrupted" {
		t.Fatalf("recovery: %+v %v", got, err)
	}
}

func TestViewerAndArtifactTraversal(t *testing.T) {
	a := testApp(t)
	c := a.Settings()
	c.Role = "viewer"
	if err := a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveLoop(testLoop(t)); err == nil {
		t.Fatal("viewer write accepted")
	}
	if _, err := a.ArtifactPath("../secret", "x"); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestCommandTimeout(t *testing.T) {
	t.Setenv("LOOPBEE_HELPER", "sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := runCommand(ctx, t.TempDir(), []string{os.Args[0], "-test.run=TestHelperSleep"}, nil)
	if err == nil {
		t.Fatal("timeout ignored")
	}
}

func TestHelperSleep(t *testing.T) {
	if os.Getenv("LOOPBEE_HELPER") == "sleep" {
		time.Sleep(time.Minute)
	}
}

func waitStatus(t *testing.T, a *App, id, status string) {
	t.Helper()
	// Generous deadline (race-instrumented helper processes start slowly), but a Run that already ended in
	// another state fails at once.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		r, e := a.Run(id)
		if e == nil && r.Status == status {
			return
		}
		if e == nil && r.Finished != nil && r.Status != status {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, _ := a.Run(id)
	t.Fatalf("want %s got %s (%s)", status, r.Status, r.Error)
}
