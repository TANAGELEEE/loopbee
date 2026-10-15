package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Every agent CLI meets the same contract with its own fake: detection, a run at each level it enforces, the four
// outcomes, the model and tokens it reports, and a refusal (not a silent downgrade) at levels it cannot enforce.
func TestEveryAdapterMeetsTheContract(t *testing.T) {
	if got := AgentNames(); !slices.Equal(got, []string{"codex", "claude", "gemini", "opencode", "pi"}) {
		t.Fatalf("adapters %v", got)
	}
	for _, name := range AgentNames() {
		t.Run(name, func(t *testing.T) {
			ad, _ := adapterFor(name)
			a := testApp(t)
			useFakeAgents(t, a, "done")
			a.DetectAgents()
			s, _ := a.State()
			var ag Agent
			for _, x := range s.Agents {
				if x.Name == name {
					ag = x
				}
			}
			if ag.Status != "available" || ag.Label != ad.Label || !slices.Equal(ag.Levels, ad.Levels) || ag.Flags["agent-config"] == "" {
				t.Fatalf("detection: %+v", ag)
			}
			setMaxSandbox(t, a, "agent-config")
			run := func(mode, level string) Run {
				t.Helper()
				t.Setenv("LOOPBEE_FAKE_MODE", mode)
				l := agentLoop(t, name)
				l.Env = nil
				l.Sandbox = level
				l.Model = "fake-model-1"
				l, err := a.SaveLoop(l)
				if err != nil {
					t.Fatal(err)
				}
				r, err := a.Start(l.ID)
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 6000; i++ {
					if r, _ = a.Run(r.ID); r.Finished != nil {
						return r
					}
					sleepMs(10)
				}
				t.Fatalf("%s at %s did not finish", mode, level)
				return r
			}
			for _, level := range ad.Levels {
				r := run("done", level)
				if r.Status != "completed" || r.Result == nil || r.Result.Summary != "Fixed 1 error, deployed to test" {
					t.Fatalf("%s: %s %q %+v", level, r.Status, r.Error, r.Result)
				}
				if r.Usage == nil || r.Usage.Total() != 1500 || r.Usage.Input != 1000 || r.Usage.CacheRead != 200 || !strings.Contains(r.Usage.Model, "fake-model-1") || r.Usage.Agent != name {
					t.Fatalf("%s usage: %+v", level, r.Usage)
				}
				if tr := r.Trace[len(r.Trace)-1]; tr.Usage == nil || tr.Usage.Total() != 1500 {
					t.Fatalf("%s step usage: %+v", level, tr.Usage)
				}
			}
			for mode, want := range map[string]string{"no_action": "no_action", "blocked": "blocked", "fail": "failed"} {
				r := run(mode, ad.Levels[0])
				if r.Status != want || (mode == "fail" && !strings.Contains(r.Error, "boom")) {
					t.Fatalf("%s: %s %q", mode, r.Status, r.Error)
				}
			}
			for _, level := range []string{"read-only", "workspace-write", "workspace-network", "agent-config"} {
				if slices.Contains(ad.Levels, level) {
					continue
				}
				l := agentLoop(t, name)
				l.Sandbox = level
				if err := Validate(l); err == nil || !strings.Contains(err.Error(), ad.Label+" cannot enforce") {
					t.Fatalf("%s accepted at %s: %v", name, level, err)
				}
			}
		})
	}
}

// What each level passes to each CLI: the enforcement loopbee promises in the editor and settings.
func TestAdapterPermissionFlags(t *testing.T) {
	dir := t.TempDir()
	reads := []string{filepath.Join(dir, "skills")}
	os.MkdirAll(reads[0], 0700)
	argsFor := func(name, level string) (string, []string) {
		ad, _ := adapterFor(name)
		argv, env := buildAgentInvocation([]string{name}, ad.Caps([]byte(fakeHelp[name])), agentInvocation{Name: name, Dir: dir, Sandbox: level, ReadPaths: reads, Scratch: dir})
		return strings.Join(argv, " "), env
	}
	cases := []struct {
		name, level string
		want, not   []string
	}{
		{"gemini", "read-only", []string{"--output-format stream-json", "--skip-trust", "--approval-mode default", "--include-directories " + reads[0], "--policy"}, []string{"--sandbox", "yolo"}},
		{"gemini", "workspace-write", []string{"--approval-mode auto_edit --sandbox", "--include-directories"}, []string{"yolo"}},
		{"gemini", "workspace-network", []string{"--approval-mode yolo --sandbox"}, []string{"--include-directories"}},
		{"gemini", "agent-config", []string{"--output-format stream-json"}, []string{"--approval-mode", "--sandbox", "--skip-trust", "--policy"}},
		{"pi", "read-only", []string{"--mode json", "--no-session", "--tools read,grep,find,ls", "--exclude-tools mcp__*"}, []string{"bash"}},
		{"pi", "agent-config", []string{"--mode json"}, []string{"--tools"}},
		{"opencode", "read-only", []string{"run --format json"}, []string{"--auto"}},
		{"opencode", "agent-config", []string{"run --format json"}, []string{"--auto"}},
	}
	for _, c := range cases {
		got, env := argsFor(c.name, c.level)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s %s: %q lacks %q", c.name, c.level, got, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s %s: %q has %q", c.name, c.level, got, n)
			}
		}
		switch {
		case c.name == "gemini" && c.level == "workspace-network":
			if len(env) != 1 || env[0] != "SANDBOX_MOUNTS="+reads[0]+":"+reads[0]+":ro" {
				t.Errorf("gemini network mounts: %v", env)
			}
		case c.name == "opencode" && c.level == "read-only":
			if len(env) != 1 || !strings.Contains(env[0], `"edit":"deny"`) || !strings.Contains(env[0], `"bash":"deny"`) || !strings.Contains(env[0], `"websearch":"deny"`) || !strings.Contains(env[0], filepath.ToSlash(reads[0])+`/**":"allow"`) {
				t.Errorf("opencode read-only config: %v", env)
			}
		case c.name == "opencode" && c.level == "agent-config":
			if len(env) != 0 {
				t.Errorf("opencode agent-config adds config: %v", env)
			}
		}
	}
	// At workspace-write Gemini's edit tools may not write the read paths, though they are workspace directories.
	argsFor("gemini", "workspace-write")
	policy, _ := os.ReadFile(filepath.Join(dir, "gemini-policy.toml"))
	if !strings.Contains(string(policy), `toolName = "activate_skill"`) || !strings.Contains(string(policy), `decision = "deny"`) || !strings.Contains(string(policy), "skills") {
		t.Fatalf("policy: %s", policy)
	}
	argsFor("gemini", "read-only")
	if policy, _ := os.ReadFile(filepath.Join(dir, "gemini-policy.toml")); strings.Contains(string(policy), "write_file") {
		t.Fatalf("read-only has no edit tools to deny: %s", policy)
	}
}

// Codex does not report its model in events; loopbee reads the default from config.toml.
func TestCodexModelFromConfig(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".codex"), 0700)
	os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("# comment\nmodel = \"gpt-fake\" # default\n\n[profiles.x]\nmodel = \"other\"\n"), 0600)
	t.Setenv("CODEX_HOME", "")
	u := usageFor("codex", []byte(`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":2}}`), "", home)
	if u == nil || u.Model != "gpt-fake" || u.Input != 6 || u.CacheRead != 4 || u.Output != 2 {
		t.Fatalf("%+v", u)
	}
	if u := usageFor("codex", []byte(`{"type":"turn.completed","usage":{}}`), "chosen", home); u.Model != "chosen" {
		t.Fatalf("the Loop's model wins: %+v", u)
	}
	var sum Usage
	sum.add(Usage{Agent: "codex", Model: "a", Input: 1, Output: 2, CostUSD: 0.5})
	sum.add(Usage{Agent: "claude", Model: "b, a", Input: 3, CacheWrite: 1})
	if sum.Model != "a, b" || sum.Agent != "codex, claude" || sum.Total() != 7 || sum.CostUSD != 0.5 {
		t.Fatalf("%+v", sum)
	}
}

func sleepMs(n int) { time.Sleep(time.Duration(n) * time.Millisecond) }

// Real pi drafted a report Loop at workspace-write, which pi cannot enforce, and the draft could not be saved.
// loopbee lowers such a level (never raises it) and says so in the reply.
func TestAuthorLowersALevelTheAgentCannotEnforce(t *testing.T) {
	a := testApp(t)
	useFakeAgents(t, a, "author") // the fake designer proposes workspace-network
	a.DetectAgents()
	setMaxSandbox(t, a, "workspace-network")
	reply, err := a.Author(t.Context(), AuthorRequest{Agent: "pi", Directory: t.TempDir(), Messages: []ChatMessage{{Role: "user", Text: "daily report"}}})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Loop.Sandbox != "read-only" || reply.Problem != "" || !strings.Contains(reply.Reply, "pi 無法強制執行「workspace-network」") {
		t.Fatalf("sandbox %q problem %q reply %q", reply.Loop.Sandbox, reply.Problem, reply.Reply)
	}
	if fallbackLevel("gemini", "agent-config") != "workspace-network" || fallbackLevel("opencode", "workspace-network") != "read-only" {
		t.Fatal("fallback levels")
	}
	if p := authorPrompt(AuthorRequest{}, "/x", "en", "UTC", "pi", "workspace-write", Loop{}); !strings.Contains(p, "For pi ONLY these are possible: read-only, agent-config") {
		t.Fatal("the designer is not told its levels")
	}
}

// Output of the real pi 1.1.0 (2026-10-13, one read-only turn with a read tool call), trimmed to the events loopbee
// reads: the final text, the result line, every turn's usage and the model must come through.
func TestRealPiOutput(t *testing.T) {
	out, err := os.ReadFile(filepath.Join("testdata", "pi-real.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := finalMessage("pi", out)
	if r := parseResult(text); r == nil || r.Status != "done" {
		t.Fatalf("final message %q", text)
	}
	u := usageFor("pi", out, "", t.TempDir())
	if u == nil || u.Model != "ls-cc/claude-opus-5-5" || u.Input != 47 || u.Output != 82 || u.CacheRead != 53 || u.CacheWrite != 24523 || u.CostUSD < 0.124 || u.CostUSD > 0.125 {
		t.Fatalf("%+v", u)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if kind, failed := classifyFor("pi", line); failed || (line != "" && kind == "agent.raw") {
			t.Fatalf("%s %v: %s", kind, failed, line)
		}
	}
	if failureFor("pi", out, "darwin") != "" {
		t.Fatal("a successful turn has no failure")
	}
}

// The real OpenCode on the developer's Mac was killed at start (exit 137); loopbee only said "not detected".
func TestDetectionSaysWhyAnAgentIsUnavailable(t *testing.T) {
	a := testApp(t)
	useFakeAgents(t, a, "done")
	t.Setenv("LOOPBEE_FAKE_BROKEN", "opencode")
	a.DetectAgents()
	s, _ := a.State()
	for _, ag := range s.Agents {
		switch ag.Name {
		case "opencode":
			if ag.Status != "unavailable" || !strings.Contains(ag.Problem, "opencode --version") || !strings.Contains(ag.Problem, "reinstall") {
				t.Fatalf("%+v", ag)
			}
		default:
			if ag.Status != "available" || ag.Problem != "" {
				t.Fatalf("%+v", ag)
			}
		}
	}
}

// The real Codex designer (0.162.1, 2026-10-13) proposed a verify step with a metric name but no JSON path, and
// “metric name and path must be provided together” stopped the draft. A half metric is dropped.
func TestDraftWithHalfAMetricIsRepaired(t *testing.T) {
	for _, half := range []Step{{Metric: "failures"}, {MetricPath: "$.failures"}} {
		half.Kind, half.Name, half.Argv = "verify", "npm test", []string{"npm", "test"}
		l := normalizeDraft(Loop{Name: "Fix tests", Timezone: "UTC", Sandbox: "read-only", Steps: []Step{{Kind: "act", Name: "Fix", Prompt: "fix"}, half}}, Loop{Directory: t.TempDir()}, "codex")
		if l.Steps[1].Metric != "" || l.Steps[1].MetricPath != "" {
			t.Fatalf("%+v", l.Steps[1])
		}
		if err := Validate(l); err != nil {
			t.Fatal(err)
		}
	}
	l := normalizeDraft(Loop{Steps: []Step{{Kind: "verify", Name: "v", Argv: []string{"x"}, Metric: "m", MetricPath: "$.m"}}}, Loop{}, "codex")
	if l.Steps[0].Metric != "m" {
		t.Fatal("a complete metric is kept")
	}
}
