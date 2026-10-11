package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestVerifyArtifactAndBudget(t *testing.T) {
	for _, mode := range []string{"success", "failure"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("LOOPBEE_HELPER", mode)
			a := testApp(t)
			l := testLoop(t)
			l.Steps = []Step{{Kind: "verify", Name: "Check", Argv: []string{os.Args[0], "-test.run=TestHelperProcess"}}}
			l.AllowedTools = []string{os.Args[0]}
			l, err := a.SaveLoop(l)
			if err != nil {
				t.Fatal(err)
			}
			r, err := a.Start(l.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "completed"
			if mode == "failure" {
				want = "budget_exhausted"
			}
			waitStatus(t, a, r.ID, want)
			r, _ = a.Run(r.ID)
			if len(r.Artifacts) != 1 {
				t.Fatal("missing artifact")
			}
			p, err := a.ArtifactPath(r.ID, r.Artifacts[0])
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(p)
			if err != nil || !bytes.Contains(b, []byte("evidence")) {
				t.Fatal("evidence missing")
			}
		})
	}
}
func TestHelperProcess(t *testing.T) {
	switch os.Getenv("LOOPBEE_HELPER") {
	case "success":
		fmt.Println("evidence")
		os.Exit(0)
	case "failure":
		fmt.Println("evidence")
		os.Exit(1)
	case "env":
		fmt.Printf("PLAIN=%s DB_PASSWORD=%s\n", os.Getenv("PLAIN"), os.Getenv("DB_PASSWORD"))
		os.Exit(0)
	case "pwd":
		wd, _ := os.Getwd()
		fmt.Println(wd)
		os.Exit(0)
	case "metric":
		fmt.Println(`{"summary":{"score":92}}`)
		os.Exit(0)
	}
}
func TestConcurrentStartAndCancel(t *testing.T) {
	a := testApp(t)
	l, err := a.SaveLoop(testLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := a.Start(l.ID); err == nil {
				ids <- r.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	var got []string
	for id := range ids {
		got = append(got, id)
	}
	if len(got) != 1 {
		t.Fatalf("started %d runs", len(got))
	}
	if err = a.Cancel(got[0]); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, a, got[0], "cancelled")
}
func TestSchedulerCatchupAndDedup(t *testing.T) {
	for _, catchup := range []bool{true, false} {
		t.Run(fmt.Sprint(catchup), func(t *testing.T) {
			a := testApp(t)
			l := testLoop(t)
			l.Enabled = true
			l.Cron = "* * * * *"
			l.CatchUp = catchup
			l, err := a.SaveLoop(l)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			l.Next = now.Add(-10 * time.Minute)
			if err = a.put("loop", l.ID, l); err != nil {
				t.Fatal(err)
			}
			a.Tick(now)
			a.Tick(now)
			runs, _ := a.Runs()
			want := 0
			if catchup {
				want = 1
			}
			if len(runs) != want {
				t.Fatalf("want %d runs got %d", want, len(runs))
			}
		})
	}
}
func TestDSTSchedule(t *testing.T) {
	l := testLoop(t)
	l.Timezone = "America/New_York"
	l.Cron = "30 2 * * *"
	s, err := schedule(l)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Date(2026, 3, 8, 6, 59, 0, 0, time.UTC)
	next := s.Next(before)
	if !next.After(before) {
		t.Fatal("next schedule moved backwards")
	}
}
func TestInstanceLock(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if b, err := Open(dir); err == nil {
		b.Close()
		t.Fatal("duplicate instance allowed")
	}
}
func TestSecretScan(t *testing.T) {
	a := testApp(t)
	l := testLoop(t)
	l.Description = "api_key=verysecretsample1234"
	if _, err := a.SaveLoop(l); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := a.Export(&b); err == nil {
		t.Fatal("secret exported")
	}
}
func TestNotificationResponses(t *testing.T) {
	for _, tc := range []struct {
		code       int
		body, want string
	}{{200, `{"code":0}`, "sent"}, {200, `{"code":19001}`, "failed"}, {429, `{}`, "retryable"}, {200, `broken`, "unknown"}, {500, `{}`, "failed"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); fmt.Fprint(w, tc.body) }))
		status, _ := sendNotification(context.Background(), Channel{Kind: "feishu", Endpoint: srv.URL}, "test")
		srv.Close()
		if status != tc.want {
			t.Fatalf("%+v got %s", tc, status)
		}
	}
}
func TestTeamSyncRoundTrip(t *testing.T) {
	t.Setenv("LOOPBEE_HELPER", "success")
	a := testApp(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	if _, err := runCommand(context.Background(), "", []string{"git", "init", "--bare", remote}, nil); err != nil {
		t.Fatal(err)
	}
	c := a.Settings()
	c.TeamRemote = remote
	if err := a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	definition := testLoop(t)
	definition.AllowedTools = []string{os.Args[0]}
	definition.Steps = []Step{{Kind: "verify", Name: "Check", Argv: []string{os.Args[0], "-test.run=TestHelperProcess"}}}
	l, err := a.SaveLoop(definition)
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.Start(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, a, run.ID, "completed")
	private := testLoop(t)
	private.Private = true
	private.Name = "Private"
	if _, err = a.SaveLoop(private); err != nil {
		t.Fatal(err)
	}
	if err = a.SyncTeam(); err != nil {
		t.Fatal(err)
	}
	b := testApp(t)
	c = b.Settings()
	c.Role = "viewer"
	c.TeamRemote = remote
	if err = b.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	if err = b.SyncTeam(); err != nil {
		t.Fatal(err)
	}
	s, err := b.State()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Loops) != 1 || s.Loops[0].ID != l.ID || s.Loops[0].Directory != "" {
		body, _ := json.Marshal(s.Loops)
		t.Fatalf("bad shared data: %s", body)
	}
	if _, err = b.Start(l.ID); err == nil {
		t.Fatal("viewer executed shared loop")
	}
	if len(s.Runs) != 1 || len(s.Runs[0].Artifacts) != 1 {
		t.Fatal("shared run evidence missing")
	}
	data, err := b.ReadArtifact(run.ID, s.Runs[0].Artifacts[0])
	if err != nil || !bytes.Contains(data, []byte("evidence")) {
		t.Fatal("shared artifact unreadable")
	}
	events, err := b.Events(run.ID)
	if err != nil || len(events) == 0 {
		t.Fatal("shared events missing")
	}
}
