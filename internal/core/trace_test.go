package core

import (
	"os"
	"testing"
	"time"
)

// The round timeline in Run details is built only from Run.Trace, so every executed step
// must leave exactly one record with its final status, artifact and gate note.
func TestRunTraceRecordsRoundsGatesAndArtifacts(t *testing.T) {
	t.Setenv("LOOPBEE_HELPER", "failure")
	a := testApp(t)
	l := testLoop(t)
	l.MaxRounds = 2
	l.StallRounds = 5
	l.AllowedTools = []string{os.Args[0]}
	l.Steps = []Step{{Kind: "gate", Name: "Review"}, {Kind: "verify", Name: "Check", Argv: []string{os.Args[0], "-test.run=TestHelperProcess"}}}
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Start(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != "manual" {
		t.Fatalf("source = %q", r.Source)
	}
	for round := 1; round <= 2; round++ {
		waitStatus(t, a, r.ID, "waiting")
		cur, _ := a.Run(r.ID)
		if last := cur.Trace[len(cur.Trace)-1]; last.Status != "waiting" || last.Round != round || last.Kind != "gate" {
			t.Fatalf("round %d gate record = %+v", round, last)
		}
		if err := a.Decide(r.ID, true, "looks fine"); err != nil {
			t.Fatal(err)
		}
		if round == 1 {
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				cur, _ = a.Run(r.ID)
				if cur.Round == 2 && cur.Status == "waiting" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	waitStatus(t, a, r.ID, "budget_exhausted")
	r, _ = a.Run(r.ID)
	if len(r.Trace) != 4 {
		t.Fatalf("want 4 trace records, got %+v", r.Trace)
	}
	want := []struct {
		round        int
		kind, status string
	}{{1, "gate", "approved"}, {1, "verify", "failed"}, {2, "gate", "approved"}, {2, "verify", "failed"}}
	for i, w := range want {
		got := r.Trace[i]
		if got.Round != w.round || got.Kind != w.kind || got.Status != w.status || got.Finished == nil {
			t.Fatalf("record %d = %+v, want %+v", i, got, w)
		}
	}
	if r.Trace[0].Note != "looks fine" || r.Trace[1].Artifact == "" || r.Trace[1].Bytes == 0 {
		t.Fatalf("gate note or artifact missing: %+v", r.Trace)
	}
}

func TestRerunAcknowledgeAndPause(t *testing.T) {
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
	if _, err := a.Rerun(r.ID); err == nil {
		t.Fatal("rerun of an active run must be rejected")
	}
	if err := a.Acknowledge(r.ID); err == nil {
		t.Fatal("only interrupted runs can be acknowledged")
	}
	if err := a.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, a, r.ID, "cancelled")
	old, _ := a.Run(r.ID)
	if old.Trace[0].Status != "cancelled" {
		t.Fatalf("cancelled gate record = %+v", old.Trace[0])
	}
	next, err := a.Rerun(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.Source != "rerun" || next.RerunOf != r.ID || next.ID == r.ID {
		t.Fatalf("rerun = %+v", next)
	}
	waitStatus(t, a, next.ID, "waiting")
	a.Cancel(next.ID)
	waitStatus(t, a, next.ID, "cancelled")

	// Interrupted runs can be acknowledged once reviewed; nothing restarts.
	broken := Run{ID: newID(), LoopID: l.ID, Definition: l, Status: "interrupted", Started: time.Now(), Artifacts: []string{}}
	if err := a.saveRun(broken); err != nil {
		t.Fatal(err)
	}
	if err := a.Acknowledge(broken.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.Run(broken.ID); !got.Acked || got.Status != "interrupted" {
		t.Fatalf("acknowledged run = %+v", got)
	}

	// Pausing skips due triggers without starting runs or recording them as missed.
	l.Cron = "* * * * *"
	l.Enabled = true
	l, err = a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Runs()
	a.Tick(l.Next.Add(time.Second))
	after, _ := a.Runs()
	if len(after) != len(before) {
		t.Fatal("paused scheduler started a run")
	}
	events, _ := a.Events("")
	for _, e := range events {
		if e.Type == "schedule.missed" {
			t.Fatal("paused trigger recorded as missed")
		}
	}
	if !a.Settings().Paused {
		t.Fatal("pause not persisted")
	}
	if err := a.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	stored, _ := a.Loops()
	a.Tick(stored[0].Next.Add(time.Second))
	runs, _ := a.Runs()
	var scheduled *Run
	for i := range runs {
		if runs[i].Source == "schedule" {
			scheduled = &runs[i]
		}
	}
	if scheduled == nil {
		t.Fatal("resumed scheduler did not start a scheduled run")
	}
	waitStatus(t, a, scheduled.ID, "waiting")
	a.Cancel(scheduled.ID)
	waitStatus(t, a, scheduled.ID, "cancelled")
}
