package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOrchestrationValidation(t *testing.T) {
	base := testLoop(t)
	base.Steps = []Step{{ID: "code", Name: "Code", Kind: "act", Prompt: "work"}, {ID: "review", Name: "Review", Kind: "review", Prompt: "check", SendBackTo: "code"}}
	cases := []struct {
		name string
		edit func(*Loop)
		want string
	}{
		{"valid", func(l *Loop) {}, ""},
		{"id uppercase", func(l *Loop) { l.Steps[0].ID = "Code" }, "use 1–40 lowercase"},
		{"id long", func(l *Loop) { l.Steps[0].ID = strings.Repeat("a", 41) }, "use 1–40 lowercase"},
		{"id punctuation", func(l *Loop) { l.Steps[0].ID = "a_b" }, "use 1–40 lowercase"},
		{"id duplicate", func(l *Loop) { l.Steps[1].ID = "code" }, "unique"},
		{"agent unknown", func(l *Loop) { l.Steps[1].Agent = "unknown" }, "supports"},
		{"agent override", func(l *Loop) { l.Steps[1].Agent = "pi"; l.Steps[1].Effort = "minimal" }, ""},
		{"effort effective", func(l *Loop) { l.Steps[1].Agent = "claude"; l.Steps[1].Effort = "minimal" }, "choose"},
		{"sandbox unsupported", func(l *Loop) { l.Steps[1].Agent = "pi"; l.Steps[1].Sandbox = "workspace-write" }, "choose"},
		{"sandbox inherited", func(l *Loop) { l.Sandbox = "workspace-write"; l.Steps[1].Agent = "pi" }, "choose"},
		{"sandbox unknown", func(l *Loop) { l.Steps[1].Sandbox = "unknown" }, "choose"},
		{"sandbox override", func(l *Loop) {
			l.Sandbox = "workspace-write"
			l.Steps[1].Agent = "pi"
			l.Steps[1].Sandbox = "read-only"
		}, ""},
		{"input valid", func(l *Loop) { l.Steps[1].Inputs = []string{"code"} }, ""},
		{"input missing", func(l *Loop) { l.Steps[1].Inputs = []string{"missing"} }, "choose its step ID"},
		{"input forward", func(l *Loop) { l.Steps[0].Inputs = []string{"review"} }, "earlier"},
		{"input self", func(l *Loop) { l.Steps[1].Inputs = []string{"review"} }, "earlier"},
		{"input gate", func(l *Loop) {
			l.Steps = append([]Step{{ID: "gate", Kind: "gate", Name: "Gate"}}, l.Steps...)
			l.Steps[2].Inputs = []string{"gate"}
		}, "earlier agent or review"},
		{"input review", func(l *Loop) {
			l.Steps = append(l.Steps, Step{ID: "report", Kind: "summarize", Name: "Report", Prompt: "report", Inputs: []string{"review"}})
		}, ""},
		{"prompt", func(l *Loop) { l.Steps[1].Prompt = " " }, "requires a prompt"},
		{"target required", func(l *Loop) { l.Steps[1].SendBackTo = "" }, "choose the ID"},
		{"target self", func(l *Loop) { l.Steps[1].SendBackTo = "review" }, "earlier observe, act or summarize"},
		{"target review", func(l *Loop) {
			l.Steps = append(l.Steps, Step{Kind: "review", Name: "Second", Prompt: "check", SendBackTo: "review"})
		}, "earlier observe, act or summarize"},
		{"attempt negative", func(l *Loop) { l.Steps[1].MaxAttempts = -1 }, "1–10"},
		{"attempt high", func(l *Loop) { l.Steps[1].MaxAttempts = 11 }, "1–10"},
		{"attempt one", func(l *Loop) { l.Steps[1].MaxAttempts = 1 }, ""},
		{"attempt ten", func(l *Loop) { l.Steps[1].MaxAttempts = 10 }, ""},
		{"sendback act", func(l *Loop) { l.Steps[0].SendBackTo = "code" }, "remove them or use review"},
		{"attempt act", func(l *Loop) { l.Steps[0].MaxAttempts = 2 }, "remove them or use review"},
		{"pr review", func(l *Loop) { l.Steps[1].PullRequest = &PullRequest{Mode: "draft"} }, "remove it or use deliver"},
		{"argv review", func(l *Loop) { l.Steps[1].Argv = []string{"echo"} }, "remove it"},
		{"metric review", func(l *Loop) { l.Steps[1].Metric = "score" }, "remove them"},
	}
	for _, field := range []string{"agent", "sandbox", "inputs", "model", "effort", "prompt"} {
		cases = append(cases, struct {
			name string
			edit func(*Loop)
			want string
		}{"gate " + field, func(l *Loop) {
			s := Step{Name: "Gate", Kind: "gate"}
			switch field {
			case "agent":
				s.Agent = "pi"
			case "sandbox":
				s.Sandbox = "read-only"
			case "inputs":
				s.Inputs = []string{"code"}
			case "model":
				s.Model = "model"
			case "effort":
				s.Effort = "high"
			case "prompt":
				s.Prompt = "hello"
			}
			l.Steps = append(l.Steps, s)
		}, "remove them or change the kind"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := base
			l.Steps = append([]Step(nil), base.Steps...)
			tc.edit(&l)
			err := Validate(l)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want %q", err, tc.want)
			}
		})
	}
}

func TestDeliverValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Loop)
		want string
	}{
		{"valid draft", func(l *Loop) {}, ""},
		{"missing pr", func(l *Loop) { l.Steps[1].PullRequest = nil }, "set its mode"},
		{"duplicate", func(l *Loop) { l.Steps = append(l.Steps, l.Steps[1]); l.Steps[2].ID = "other" }, "at most one"},
		{"legacy conflict", func(l *Loop) { l.PullRequest = &PullRequest{Mode: "draft"} }, "remove Loop.pullRequest"},
		{"workspace", func(l *Loop) { l.Workspace = "direct"; l.Branch = "" }, "set workspace"},
		{"branch", func(l *Loop) { l.Branch = "" }, "provide branch"},
		{"mode", func(l *Loop) { l.Steps[1].PullRequest.Mode = "bad" }, "must be after-review or draft"},
		{"base", func(l *Loop) { l.Steps[1].PullRequest.Base = "bad branch" }, "use a branch name"},
		{"merge", func(l *Loop) { l.Steps[1].PullRequest.Merge = "bad" }, "must be empty or after-checks"},
		{"draft merge", func(l *Loop) { l.Steps[1].PullRequest.Merge = "after-checks" }, "needs “after my review”"},
		{"gate required", func(l *Loop) { l.Steps[1].PullRequest.Mode = "after-review" }, "needs a gate after"},
		{"late gate", func(l *Loop) {
			l.Steps[1].PullRequest.Mode = "after-review"
			l.Steps = append(l.Steps, Step{Name: "Gate", Kind: "gate"})
		}, "needs a gate after"},
		{"approved", func(l *Loop) {
			l.Steps[1].PullRequest.Mode = "after-review"
			l.Steps[1].PullRequest.Merge = "after-checks"
			l.Steps = append(l.Steps[:1], Step{Name: "Gate", Kind: "gate"}, l.Steps[1])
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := testLoop(t)
			l.Workspace = "worktree"
			l.Branch = "loopbee/test"
			l.Steps = []Step{{ID: "code", Name: "Code", Kind: "act", Prompt: "work"}, {ID: "deliver", Name: "Deliver", Kind: "deliver", PullRequest: &PullRequest{Mode: "draft"}}}
			tc.edit(&l)
			err := Validate(l)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want %q", err, tc.want)
			}
		})
	}
}

func TestSaveLoopStepIDs(t *testing.T) {
	a := testApp(t)
	l := testLoop(t)
	l.Steps = []Step{{Name: "Write Code", Kind: "act", Prompt: "work"}, {Name: "Write Code", Kind: "gate"}, {Name: "中文", Kind: "gate"}, {ID: "step-3", Name: "Existing", Kind: "gate"}, {ID: "review", Name: "Review", Kind: "review", Prompt: "check", SendBackTo: "write-code"}}
	saved, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"write-code", "step-2", "step-4", "step-3", "review"}
	for i, s := range saved.Steps {
		if s.ID != want[i] {
			t.Fatalf("step %d: %q", i, s.ID)
		}
	}
	if saved.Steps[4].MaxAttempts != 3 {
		t.Fatal("default attempts not normalized")
	}
	saved.Steps[0].Name = "Renamed"
	again, err := a.SaveLoop(saved)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range again.Steps {
		if s.ID != want[i] {
			t.Fatal("ID changed")
		}
	}
	again.Steps[4].Sandbox = "workspace-write"
	if _, err = a.SaveLoop(again); err == nil || !strings.Contains(err.Error(), "lower the step sandbox or raise") {
		t.Fatalf("device ceiling: %v", err)
	}
}

func TestOpenBackfillsStepIDs(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l := testLoop(t)
	l.ID = "old"
	l.Version = 7
	l.Project = "existing"
	l.Steps = append(l.Steps, Step{ID: "kept", Name: "Existing", Kind: "gate"})
	if err = a.put("loop", l.ID, l); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var got Loop
	if err = a.get("loop", l.ID, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 7 || got.Steps[0].ID != "review" || got.Steps[1].ID != "kept" || !got.Updated.Equal(l.Updated) {
		t.Fatalf("bad migration: %+v", got)
	}
}

func TestOrchestrationJSONRoundTrip(t *testing.T) {
	original := Run{Definition: Loop{Steps: []Step{{ID: "review", Kind: "review", Agent: "pi", Sandbox: "read-only", Inputs: []string{"code"}, SendBackTo: "code", MaxAttempts: 3}, {ID: "deliver", Kind: "deliver", PullRequest: &PullRequest{Mode: "draft", Base: "test"}}}}, Trace: []StepRecord{{Attempt: 2, Agent: "pi"}}}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var got Run
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, got) {
		t.Fatalf("roundtrip: %s", data)
	}
	for _, key := range []string{"id", "agent", "sandbox", "inputs", "sendBackTo", "maxAttempts", "pullRequest", "attempt"} {
		if !strings.Contains(string(data), `"`+key+`":`) {
			t.Fatalf("missing key %s", key)
		}
	}
	data, _ = json.Marshal(Step{})
	for _, key := range []string{"id", "agent", "sandbox", "inputs", "sendBackTo", "maxAttempts", "pullRequest"} {
		if strings.Contains(string(data), `"`+key+`":`) {
			t.Fatalf("not omitted: %s", key)
		}
	}
}
