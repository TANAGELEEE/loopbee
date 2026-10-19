package core

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func authorReviewDraft() Loop {
	return Loop{Name: "Fix and review", Sandbox: "workspace-write", Workspace: "worktree", Branch: "loopbee/fix-{run}", Steps: []Step{
		{ID: "fix", Kind: "act", Name: "Fix", Prompt: "Fix the problem"},
		{ID: "review", Kind: "review", Name: "Review", Prompt: "Review the fix", Agent: "pi", Sandbox: "read-only", Inputs: []string{"fix"}, SendBackTo: "fix", MaxAttempts: 3},
		{ID: "approve", Kind: "gate", Name: "Approve"},
		{ID: "deliver", Kind: "deliver", Name: "Deliver", PullRequest: &PullRequest{Mode: "after-review", Base: "main", Merge: "after-checks"}},
	}}
}

func TestAuthorReviewSchemaAndNestedDraft(t *testing.T) {
	schema := authorSchema()["properties"].(map[string]any)["loop"].(map[string]any)
	props := schema["properties"].(map[string]any)
	step := props["steps"].(map[string]any)["items"].(map[string]any)
	fields := step["properties"].(map[string]any)
	for _, name := range []string{"id", "agent", "sandbox", "inputs", "sendBackTo", "maxAttempts", "pullRequest"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("step schema lacks %s", name)
		}
	}
	for _, kind := range []string{"review", "deliver"} {
		if !slices.Contains(fields["kind"].(map[string]any)["enum"].([]string), kind) {
			t.Errorf("step schema lacks kind %s", kind)
		}
	}
	if !reflect.DeepEqual(fields["pullRequest"], props["pullRequest"]) {
		t.Error("step and Loop pull request schemas differ")
	}
	l := authorReviewDraft()
	l.Env = []EnvVar{{Name: "TOKEN", Value: "keyring:hidden", Secret: true}}
	b, err := json.Marshal(definitionForAgent(l))
	if err != nil {
		t.Fatal(err)
	}
	var shown any
	if err := json.Unmarshal(b, &shown); err != nil {
		t.Fatal(err)
	}
	var check func(map[string]any, any, string)
	check = func(schema map[string]any, value any, path string) {
		switch schema["type"] {
		case "object":
			object, ok := value.(map[string]any)
			if !ok {
				t.Errorf("%s must expose an object, got %T", path, value)
				return
			}
			for key, child := range schema["properties"].(map[string]any) {
				v, ok := object[key]
				if !ok {
					t.Errorf("draft lacks %s.%s", path, key)
					continue
				}
				if !slices.Contains(schema["required"].([]string), key) {
					t.Errorf("strict schema does not require %s.%s", path, key)
				}
				check(child.(map[string]any), v, path+"."+key)
			}
		case "array":
			items, ok := value.([]any)
			if !ok {
				t.Errorf("%s must expose an array, got %T", path, value)
				return
			}
			for _, item := range items {
				check(schema["items"].(map[string]any), item, path+"[]")
			}
		}
	}
	check(schema, shown, "loop")
	var roundTrip Loop
	if err := json.Unmarshal(b, &roundTrip); err != nil {
		t.Fatal(err)
	}
	got := normalizeDraft(roundTrip, Loop{Directory: t.TempDir()}, "codex")
	if !reflect.DeepEqual(got.Steps, l.Steps) {
		t.Fatalf("step fields lost in draft: got %+v, want %+v", got.Steps, l.Steps)
	}
	if err := Validate(got); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorAutomaticDeliverySchemaAndDraft(t *testing.T) {
	props := authorSchema()["properties"].(map[string]any)["loop"].(map[string]any)["properties"].(map[string]any)
	prSchema := props["pullRequest"].(map[string]any)
	fields := prSchema["properties"].(map[string]any)
	if !slices.Contains(fields["mode"].(map[string]any)["enum"].([]string), "automatic") {
		t.Error("pull request schema lacks automatic mode")
	}
	if !reflect.DeepEqual(fields["reviewPaths"], map[string]any{"type": "array", "items": map[string]any{"type": "string"}}) || !slices.Contains(prSchema["required"].([]string), "reviewPaths") {
		t.Error("strict pull request schema must require reviewPaths as a string array")
	}
	p := authorReviewDraft()
	p.Steps = append(p.Steps[:2], p.Steps[3])
	p.Steps[2].PullRequest = &PullRequest{Mode: "automatic", Base: "test", Merge: "after-checks", ReviewPaths: []string{"deploy/k8s/**"}}
	b, err := json.Marshal(definitionForAgent(p))
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Loop
	if err := json.Unmarshal(b, &roundTrip); err != nil {
		t.Fatal(err)
	}
	got := normalizeDraft(roundTrip, Loop{}, "codex")
	if !reflect.DeepEqual(got.Steps, p.Steps) || got.PullRequest != nil {
		t.Fatalf("automatic delivery fields lost: %+v", got)
	}
	got.Directory = t.TempDir()
	if err := Validate(got); err != nil {
		t.Fatal(err)
	}
	for _, pr := range []*PullRequest{{}, {ReviewPaths: []string{}}, {ReviewPaths: []string{"deploy/k8s/**"}}} {
		p := Loop{PullRequest: pr, Steps: []Step{{Kind: "deliver", PullRequest: pr}}}
		got := normalizeDraft(p, Loop{}, "codex")
		for _, normalized := range []*PullRequest{got.PullRequest, got.Steps[0].PullRequest} {
			if len(pr.ReviewPaths) == 0 && normalized != nil || len(pr.ReviewPaths) != 0 && !reflect.DeepEqual(normalized, pr) {
				t.Fatalf("normalization must preserve nonempty reviewPaths and drop only empty placeholders: %+v", normalized)
			}
		}
	}
}

func TestAuthorNormalizesEmptyReviewPathsWithoutMutatingProposal(t *testing.T) {
	pr := &PullRequest{Mode: "draft", ReviewPaths: []string{}}
	got := normalizeDraft(Loop{PullRequest: pr, Steps: []Step{{Kind: "deliver", PullRequest: pr}}}, Loop{}, "codex")
	for _, normalized := range []*PullRequest{got.PullRequest, got.Steps[0].PullRequest} {
		if normalized == nil || normalized.Mode != "draft" || normalized.ReviewPaths != nil {
			t.Fatalf("empty reviewPaths should normalize to nil without dropping configured PR: %+v", normalized)
		}
	}
	if pr.ReviewPaths == nil {
		t.Fatal("normalization mutated the caller's pull request")
	}
}

func TestAuthorAutomaticDeliveryPrompt(t *testing.T) {
	prompt := authorPrompt(AuthorRequest{}, "", "en", "UTC", "codex", "workspace-write", Loop{})
	for _, want := range []string{
		`"automatic" is allowed only on a deliver step`, `explicit non-default test branch`, `merge "after-checks" is mandatory`,
		`default branch is never an automatic target`, `reviewPaths`, `repository-root-relative`, `* and ? match within one path segment`, `** matches zero or more path segments`,
		`Confirm the ConfigMap YAML paths with the person`, `SQL migrations`, `repository's existing CI/CD`,
		`Changes under .github/ are still refused`, `never authorizes local run/verify commands to execute agent-changed files`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func TestAuthorReviewNormalizesOnlyLeftovers(t *testing.T) {
	base := Loop{Directory: t.TempDir()}
	for _, kind := range []string{"act", "observe", "summarize", "review", "run", "verify", "gate", "deliver"} {
		t.Run(kind, func(t *testing.T) {
			p := authorReviewDraft()
			p.Steps = append(p.Steps, Step{Kind: kind, Name: kind, PullRequest: &PullRequest{}})
			p.PullRequest = &PullRequest{}
			got := normalizeDraft(p, base, "codex")
			if got.PullRequest != nil || got.Steps[len(got.Steps)-1].PullRequest != nil {
				t.Fatal("empty strict-schema pull requests must be dropped")
			}
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*Loop)
		want string
	}{
		{"unknown input", func(l *Loop) { l.Steps[1].Inputs = []string{"missing"} }, "inputs"},
		{"forward input", func(l *Loop) { l.Steps[0].Inputs = []string{"review"} }, "inputs"},
		{"empty input id", func(l *Loop) { l.Steps[1].Inputs = []string{""} }, "inputs"},
		{"gate input", func(l *Loop) { l.Steps[2].Inputs = []string{"fix"} }, "inputs"},
		{"unknown send back", func(l *Loop) { l.Steps[1].SendBackTo = "missing" }, "sendBackTo"},
		{"forward send back", func(l *Loop) { l.Steps[1].SendBackTo = "deliver" }, "sendBackTo"},
		{"self send back", func(l *Loop) { l.Steps[1].SendBackTo = "review" }, "sendBackTo"},
		{"review send back", func(l *Loop) {
			s := l.Steps[1]
			s.ID, s.SendBackTo = "second-review", "review"
			l.Steps = append(l.Steps[:2], s)
		}, "sendBackTo"},
		{"command input", func(l *Loop) {
			l.Steps[0] = Step{ID: "fix", Kind: "run", Name: "Check", Argv: []string{"go", "test"}}
		}, "inputs"},
		{"duplicate id", func(l *Loop) { l.Steps[1].ID = "fix" }, "id must be unique"},
		{"missing delivery PR", func(l *Loop) { l.Steps[3].PullRequest = nil }, "deliver requires pullRequest"},
		{"missing send back", func(l *Loop) { l.Steps[1].SendBackTo = "" }, "sendBackTo"},
		{"non-review send back", func(l *Loop) { l.Steps[0].SendBackTo = "fix" }, "sendBackTo"},
		{"too many attempts", func(l *Loop) { l.Steps[1].MaxAttempts = 11 }, "maxAttempts"},
		{"negative attempts", func(l *Loop) { l.Steps[1].MaxAttempts = -1 }, "maxAttempts"},
		{"partial step PR", func(l *Loop) { l.Steps[3].PullRequest = &PullRequest{Base: "main"} }, "mode"},
		{"partial Loop PR", func(l *Loop) { l.PullRequest = &PullRequest{Base: "main"} }, "mode"},
		{"two deliveries", func(l *Loop) { s := l.Steps[3]; s.ID = "again"; l.Steps = append(l.Steps, s) }, "at most one deliver"},
		{"Loop and step PR", func(l *Loop) { l.PullRequest = &PullRequest{Mode: "draft"} }, "remove Loop.pullRequest"},
		{"no human gate", func(l *Loop) { l.Steps = append(l.Steps[:2], l.Steps[3]) }, "gate after the last agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := authorReviewDraft()
			tc.edit(&p)
			got := normalizeDraft(p, base, "codex")
			if err := Validate(got); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid proposal was hidden: got %v, want %s", err, tc.want)
			}
		})
	}
	for _, attempts := range []int{0, 1, 10} {
		p := authorReviewDraft()
		p.Steps[1].MaxAttempts = attempts
		if err := Validate(normalizeDraft(p, base, "codex")); err != nil {
			t.Fatalf("valid maxAttempts %d: %v", attempts, err)
		}
	}
	p := authorReviewDraft()
	p.Steps[0].PullRequest = &PullRequest{}
	normalizeDraft(p, base, "codex")
	if p.Steps[0].PullRequest == nil {
		t.Fatal("normalization mutated the caller's steps")
	}
}

func TestAuthorReviewProposalAcceptedAndPrompt(t *testing.T) {
	a := testApp(t)
	log := useFakeAgents(t, a, "report")
	a.DetectAgents()
	setMaxSandbox(t, a, "workspace-write")
	proposal := authorReviewDraft()
	b, err := json.Marshal(map[string]any{"reply": "Review then deliver", "loop": definitionForAgent(proposal)})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOPBEE_FAKE_REPORT", string(b))
	req := AuthorRequest{Agent: "codex", Directory: t.TempDir(), Messages: []ChatMessage{{Role: "user", Text: "Fix, review, and deliver"}}}
	reply, err := a.Author(t.Context(), req)
	if err != nil || reply.Problem != "" {
		t.Fatalf("proposal: %v / %s", err, reply.Problem)
	}
	if !reflect.DeepEqual(reply.Loop.Steps, proposal.Steps) || reply.Loop.Enabled || !reply.Loop.Private {
		t.Fatalf("proposal changed: %+v", reply.Loop)
	}
	if _, err := a.SaveLoop(reply.Loop); err != nil {
		t.Fatal(err)
	}
	prompt := fakeCalls(t, log)[0]["prompt"].(string)
	for _, want := range []string{"changes_requested", "sendBackTo", "earlier observe, act or summarize", "maxAttempts", "1–10", "default 3", "report", "inputs", "at most one deliver", "Loop.pullRequest", "not a human approval", "step → Loop → CLI", "Detected agents", "pi", "read-only, agent-config", "bounds send-backs", "A step that writes files needs an agent whose enforced levels", "then continues with the following steps", "squash-merges the pull request before continuing"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	for _, name := range AgentNames() {
		ag, _ := a.agentInfo(name)
		if !strings.Contains(prompt, ag.Name+" ("+ag.Label+"): available; enforces: "+strings.Join(ag.Levels, ", ")) {
			t.Errorf("prompt omitted detected agent's levels: %+v", ag)
		}
	}
	proposal.Steps[1].Inputs = []string{"missing"}
	b, _ = json.Marshal(map[string]any{"loop": definitionForAgent(proposal)})
	t.Setenv("LOOPBEE_FAKE_REPORT", string(b))
	reply, err = a.Author(t.Context(), req)
	if err != nil || !strings.Contains(reply.Problem, "inputs") {
		t.Fatalf("invalid reference: %v / %s", err, reply.Problem)
	}
	proposal.Steps[1].Inputs = []string{"fix"}
	proposal.Steps[1].Agent, proposal.Steps[1].Sandbox = "codex", "workspace-network"
	b, _ = json.Marshal(map[string]any{"loop": definitionForAgent(proposal)})
	t.Setenv("LOOPBEE_FAKE_REPORT", string(b))
	reply, err = a.Author(t.Context(), req)
	if err != nil || !strings.Contains(reply.Problem, "device's limit") {
		t.Fatalf("step ceiling: %v / %s", err, reply.Problem)
	}
}

func TestAuthorBeectlReviewExampleValid(t *testing.T) {
	b, err := os.ReadFile("../../cmd/beectl/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, example, ok := strings.Cut(strings.ReplaceAll(string(b), "\r\n", "\n"), "## Act, review and deliver")
	if !ok {
		t.Fatal("skill lacks act-review-deliver example")
	}
	_, example, ok = strings.Cut(example, "```json\n")
	if !ok {
		t.Fatal("skill lacks JSON example")
	}
	example, _, _ = strings.Cut(example, "```")
	var l Loop
	if err := json.Unmarshal([]byte(example), &l); err != nil {
		t.Fatal(err)
	}
	l.Directory = t.TempDir()
	if err := Validate(l); err != nil {
		t.Fatal(err)
	}
}
