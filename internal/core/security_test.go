package core

import (
	"testing"
	"time"
)

func TestSensitiveContentScanner(t *testing.T) {
	if !sensitive([]byte(`{"text":"{\"token\":\"nested-secret-value\"}"}`)) {
		t.Fatal("escaped JSON secret missed")
	}
	for _, s := range []string{`{"api_key":"private-test-value"}`, `password=private-test-value`, `person@example.com`, `-----BEGIN RSA PRIVATE KEY-----`} {
		if !secretPattern.MatchString(s) {
			t.Fatalf("scanner missed %s", s)
		}
	}
}

func TestAgentFailedAndUnknownEvents(t *testing.T) {
	kind, failed := classifyAgentEvent(`{"type":"turn.failed","error":{"message":"failure"}}`)
	if kind != "agent.turn.failed" || !failed {
		t.Fatal("agent failure ignored")
	}
	kind, failed = classifyAgentEvent(`{"type":"future.unknown","evidence":"keep"}`)
	if kind != "agent.raw" || failed {
		t.Fatal("unknown event misclassified")
	}
}
func TestPermissionCeiling(t *testing.T) {
	a := testApp(t)
	l := testLoop(t)
	l.Sandbox = "workspace-write"
	if _, err := a.SaveLoop(l); err == nil {
		t.Fatal("permission ceiling bypassed")
	}
	c := a.Settings()
	c.MaxSandbox = "workspace-write"
	if err := a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	saved, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	c.MaxSandbox = "read-only"
	if err = a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Start(saved.ID); err == nil {
		t.Fatal("saved definition bypassed lowered ceiling")
	}
}
func TestOptimisticVersion(t *testing.T) {
	a := testApp(t)
	l, err := a.SaveLoop(testLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.SaveLoop(l); err != nil {
		t.Fatal(err)
	}
	if _, err = a.SaveLoop(l); err == nil {
		t.Fatal("stale edit silently overwrote definition")
	}
}
func TestPrunePreservesActiveRuns(t *testing.T) {
	a := testApp(t)
	old := time.Now().AddDate(0, 0, -50)
	done := Run{ID: newID(), Status: "completed", Started: old, Finished: &old, Artifacts: []string{}}
	waiting := Run{ID: newID(), Status: "waiting", Started: old}
	if err := a.saveRun(done); err != nil {
		t.Fatal(err)
	}
	if err := a.saveRun(waiting); err != nil {
		t.Fatal(err)
	}
	n, err := a.Prune()
	if err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
	if _, err = a.Run(waiting.ID); err != nil {
		t.Fatal("active run removed")
	}
}

func TestRetryCannotBeReplayed(t *testing.T) {
	a := testApp(t)
	d := Delivery{ID: newID(), Channel: "desktop", Text: "Controlled test", Status: "unknown"}
	if err := a.put("delivery", d.ID, d); err != nil {
		t.Fatal(err)
	}
	if err := a.RetryDelivery(d.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.RetryDelivery(d.ID); err == nil {
		t.Fatal("original delivery retried twice")
	}
}
