package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestFakeAgent is not a test: it is the fake Codex / Claude Code CLI used by the tests below.
// It is selected by LOOPBEE_FAKE_AGENT and behaves according to LOOPBEE_FAKE_MODE.
func TestFakeAgent(t *testing.T) {
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	name := ""
	if len(args) > 0 && strings.HasPrefix(args[0], "@fake=") {
		name, args = strings.TrimPrefix(args[0], "@fake="), args[1:]
	}
	if name == "" {
		name = os.Getenv("LOOPBEE_FAKE_AGENT")
	}
	if name == "" {
		return
	}
	has := func(s string) bool { return slices.Contains(args, s) }
	if os.Getenv("LOOPBEE_FAKE_BROKEN") == name {
		os.Exit(137) // like a binary macOS kills at start
	}
	if has("--version") {
		fmt.Println(name + " fake 1.0")
		os.Exit(0)
	}
	if has("--help") {
		fmt.Println(fakeHelp[name])
		os.Exit(0)
	}
	if name == "codex" && has("mcp") && has("list") { // `codex mcp list --json`
		fmt.Println(orDefault(os.Getenv("LOOPBEE_FAKE_MCP_LIST"), "[]"))
		os.Exit(0)
	}
	prompt, _ := io.ReadAll(os.Stdin)
	cwd, _ := os.Getwd()
	if log := os.Getenv("LOOPBEE_FAKE_LOG"); log != "" {
		b, _ := json.Marshal(map[string]any{"args": args, "prompt": string(prompt), "cwd": cwd, "secret": os.Getenv("FAKE_SECRET")})
		f, _ := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		f.Write(append(b, '\n'))
		f.Close()
	}
	mode := os.Getenv("LOOPBEE_FAKE_MODE")
	text := "Checked the logs and fixed checkout-timeout.\n" + resultMarker + ` {"status":"done","summary":"Fixed 1 error, deployed to test","links":["https://example.com/pr/7"]}`
	var structured any
	switch mode {
	case "no_action":
		text = "Logs are clean.\n" + resultMarker + ` {"status":"no_action","summary":"No actionable errors"}`
	case "blocked":
		text = resultMarker + ` {"status":"blocked","summary":"Cannot reach the log service"}`
	case "plain":
		text = "Done, no structured result."
	case "fail":
		fmt.Println(fakeFailure[name])
		os.Exit(1)
	case "author":
		proposal := map[string]any{
			"reply":     "Here is a nightly Loop.",
			"questions": []map[string]any{{"text": "Who merges to master?", "options": []string{"Loopbee after approval", "I do it myself"}}},
			"loop": map[string]any{
				"name": "Fix production errors", "description": "Nightly log sweep", "cron": "0 2 * * *", "timezone": fakeTimezone(),
				"sandbox": "workspace-network", "workspace": "worktree", "baseRef": "origin/master", "lock": "env:test", "maxRounds": 1,
				"wallMinutes": 90, "stallRounds": 2, "gateHours": 48, "doneWhen": "verify.passed", "allowedTools": []string{},
				"steps": []map[string]any{
					{"kind": "act", "name": "Fix and deploy to test", "prompt": "Use /fix-prod-errors", "argv": []string{"ignored"}, "metric": "", "metricPath": ""},
					{"kind": "gate", "name": "Approve production", "prompt": "", "argv": []string{}, "metric": "", "metricPath": ""},
				},
				"notify": map[string]any{"events": []string{"done", "attention", "failed"}, "includeReport": true},
				"env":    []map[string]any{{"name": "LOG_TOKEN", "secret": true, "value": "leaked"}},
			},
		}
		b, _ := json.Marshal(proposal)
		text = "```json\n" + string(b) + "\n```"
		if has("--json-schema") {
			structured = proposal
		}
	case "limit":
		// Real Claude Code 2.1.296 output when the subscription's session limit is reached.
		fmt.Println(`{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"api_error":"usage_limit_reached","result":"You've hit your session limit · resets 10:20pm (Asia/Taipei)"}`)
		os.Exit(1)
	case "sleep":
		time.Sleep(10 * time.Second)
	case "edit":
		os.WriteFile(filepath.Join(cwd, "fixed.txt"), []byte("fixed by the agent\n"), 0600)
	case "partial": // started the fix, then stopped with a question
		os.WriteFile(filepath.Join(cwd, "fixed.txt"), []byte("half done\n"), 0600)
		text = resultMarker + ` {"status":"blocked","summary":"Should the old flag stay?"}`
	// Hostile agents (a prompt-injected issue): each tries to get code run outside its sandbox.
	case "tamper":
		os.WriteFile(filepath.Join(cwd, "tool.txt"), []byte("changed by the agent\n"), 0600)
	case "hook":
		os.MkdirAll(filepath.Join(cwd, ".husky"), 0700)
		os.WriteFile(filepath.Join(cwd, ".husky", "pre-commit"), []byte("#!/bin/sh\ntouch "+os.Getenv("LOOPBEE_FAKE_MARKER")+"\n"), 0700)
		os.WriteFile(filepath.Join(cwd, ".husky", "post-commit"), []byte("#!/bin/sh\ntouch "+os.Getenv("LOOPBEE_FAKE_MARKER")+"\n"), 0700)
	case "gitfile":
		os.WriteFile(filepath.Join(cwd, ".git"), []byte("gitdir: /tmp/agent-controlled\n"), 0600)
	case "ci":
		os.MkdirAll(filepath.Join(cwd, ".github", "workflows"), 0700)
		os.WriteFile(filepath.Join(cwd, ".github", "workflows", "x.yml"), []byte("on: push\n"), 0600)
	case "missing":
		text = "Could not load the credentials.\n" + resultMarker + ` {"status":"blocked","summary":"nexscope credentials unreadable","missing":["~/.pi/agent/private/nexscope.json (EPERM)","network access to api.example.com"]}`
	case "nosandbox": // real Claude Code output when its sandbox cannot start
		fmt.Println(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["Sandbox required but unavailable: sandbox is enabled but the Windows sandbox is not active on this session (feature gate off). Set sandbox.failIfUnavailable=false to allow unsandboxed execution."]}`)
		os.Exit(1)
	case "unignore": // edits a copied, ignored file and un-ignores it, hoping Loopbee commits it
		env, err := os.ReadFile(filepath.Join(cwd, ".env"))
		local, err2 := os.ReadFile(filepath.Join(cwd, "CLAUDE.local.md"))
		if err != nil || err2 != nil {
			text = resultMarker + ` {"status":"blocked","summary":"copied files missing","missing":[".env"]}`
			break
		}
		os.WriteFile(filepath.Join(cwd, "seen.txt"), append(env, local...), 0600)
		os.WriteFile(filepath.Join(cwd, ".env"), []byte("API_KEY=changed\n"), 0600)
		os.WriteFile(filepath.Join(cwd, ".gitignore"), []byte("# nothing ignored\n"), 0600)
	case "secret":
		os.WriteFile(filepath.Join(cwd, "config.txt"), []byte("token = ghp_"+strings.Repeat("a1B2", 9)+"\n"), 0600)
	case "mcp": // the task needs the user's browser MCP server "chrome"; each CLI refuses it unless approved in advance
		all := strings.Join(args, " ")
		approved := map[string]bool{
			"codex":  strings.Contains(all, `mcp_servers.chrome.default_tools_approval_mode="approve"`),
			"claude": strings.Contains(all, `"allow":["mcp__*"`),
		}[name]
		if approved {
			text = "Updated the ConfigMap in the console.\n" + resultMarker + ` {"status":"done","summary":"ConfigMap updated through chrome"}`
			break
		}
		switch name {
		case "codex": // real Codex exec refusal (approval_policy is always "never" in exec)
			fmt.Println(`{"type":"item.completed","item":{"id":"i0","type":"mcp_tool_call","server":"chrome","tool":"navigate","arguments":{},"result":null,"error":{"message":"rejected: MCP tool call requires approval, but approval policy is never"},"status":"failed"}}`)
		case "claude":
			fmt.Println(`{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"Claude requested permissions to use mcp__chrome__navigate, but you haven't granted it yet."}]}}`)
		}
		text = resultMarker + ` {"status":"blocked","summary":"The console could not be opened"}`
	}
	fakeOutput(name, args, text, structured)
	os.Exit(0)
}

// Help text of each fake CLI: the flags Loopbee looks for.
var fakeHelp = map[string]string{
	"codex":    "--json --sandbox --output-last-message --output-schema --add-dir --skip-git-repo-check --model",
	"claude":   "--print --output-format stream-json --json-schema --add-dir --tools --settings --permission-mode --model",
	"gemini":   "--output-format [choices: text, json, stream-json] --approval-mode --sandbox --policy --include-directories --skip-trust --model",
	"pi":       "--mode json --tools --exclude-tools --no-session --model",
	"opencode": "--format json --model --auto",
}

// Each CLI's real way of reporting a failed turn.
var fakeFailure = map[string]string{
	"codex":    `{"type":"turn.failed","error":{"message":"boom"}}`,
	"claude":   `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom"}`,
	"gemini":   `{"type":"result","timestamp":"t","status":"error","error":{"type":"FatalError","message":"boom"}}`,
	"pi":       `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"boom"}}` + "\n" + `{"type":"agent_end","willRetry":false,"messages":[{"role":"assistant","content":[],"stopReason":"error","errorMessage":"boom"}]}`,
	"opencode": `{"type":"error","timestamp":1,"sessionID":"s","error":{"name":"UnknownError","data":{"message":"boom"}}}`,
}

// fakeOutput prints text the way each CLI streams it, with the usage each reports. Every CLI reports the same
// turn: 1,000 fresh input tokens, 200 cached, 300 output (OpenCode: 250 output + 50 reasoning).
func fakeOutput(name string, args []string, text string, structured any) {
	emit := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	model := ""
	for i, a := range args {
		if (a == "--model" || a == "-m") && i+1 < len(args) {
			model = args[i+1]
		}
	}
	switch name {
	case "codex":
		emit(map[string]any{"type": "thread.started", "thread_id": "t1"})
		emit(map[string]any{"type": "item.completed", "item": map[string]any{"id": "i1", "type": "agent_message", "text": text}})
		emit(map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": 1200, "cached_input_tokens": 200, "output_tokens": 300}})
		for i, a := range args {
			if a == "--output-last-message" && i+1 < len(args) {
				os.WriteFile(args[i+1], []byte(text), 0600)
			}
		}
	case "claude":
		emit(map[string]any{"type": "system", "subtype": "init", "model": orDefault(model, "claude-fake")})
		emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}})
		res := map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": text, "total_cost_usd": 0.0123,
			"usage": map[string]any{"input_tokens": 1000, "cache_read_input_tokens": 200, "cache_creation_input_tokens": 0, "output_tokens": 300}}
		if structured != nil {
			res["structured_output"] = structured
		}
		emit(res)
	case "gemini":
		m := orDefault(model, "gemini-fake")
		emit(map[string]any{"type": "init", "timestamp": "t", "session_id": "s", "model": m})
		emit(map[string]any{"type": "message", "timestamp": "t", "role": "user", "content": "prompt"})
		emit(map[string]any{"type": "tool_use", "timestamp": "t", "tool_name": "read_file", "tool_id": "1", "parameters": map[string]any{}})
		emit(map[string]any{"type": "message", "timestamp": "t", "role": "assistant", "content": "ignored before the tool call", "delta": true})
		emit(map[string]any{"type": "tool_result", "timestamp": "t", "tool_id": "1", "status": "success"})
		half := len(text) / 2
		emit(map[string]any{"type": "message", "timestamp": "t", "role": "assistant", "content": text[:half], "delta": true})
		emit(map[string]any{"type": "message", "timestamp": "t", "role": "assistant", "content": text[half:], "delta": true})
		emit(map[string]any{"type": "result", "timestamp": "t", "status": "success", "stats": map[string]any{
			"total_tokens": 1500, "input_tokens": 1200, "output_tokens": 300, "cached": 200, "input": 1000, "duration_ms": 10, "tool_calls": 1,
			"models": map[string]any{m: map[string]any{"total_tokens": 1500, "input_tokens": 1200, "output_tokens": 300, "cached": 200, "input": 1000}}}})
	case "pi":
		m := orDefault(model, "fake-model")
		emit(map[string]any{"type": "session", "version": 3, "id": "s", "cwd": "/"})
		emit(map[string]any{"type": "agent_start"})
		msg := map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": text}}, "provider": "anthropic", "model": m,
			"usage": map[string]any{"input": 1000, "output": 300, "cacheRead": 200, "cacheWrite": 0, "totalTokens": 1500, "cost": map[string]any{"total": 0.01}}, "stopReason": "stop"}
		emit(map[string]any{"type": "message_end", "message": msg})
		emit(map[string]any{"type": "agent_end", "willRetry": false, "messages": []any{msg}})
		emit(map[string]any{"type": "agent_settled", "aborted": false})
	case "opencode":
		emit(map[string]any{"type": "step_start", "timestamp": 1, "sessionID": "s", "part": map[string]any{"type": "step-start"}})
		emit(map[string]any{"type": "text", "timestamp": 1, "sessionID": "s", "part": map[string]any{"type": "text", "text": "before the tool"}})
		emit(map[string]any{"type": "tool_use", "timestamp": 1, "sessionID": "s", "part": map[string]any{"type": "tool", "tool": "read"}})
		emit(map[string]any{"type": "text", "timestamp": 1, "sessionID": "s", "part": map[string]any{"type": "text", "text": text}})
		emit(map[string]any{"type": "step_finish", "timestamp": 1, "sessionID": "s", "part": map[string]any{"type": "step-finish", "cost": 0.01,
			"tokens": map[string]any{"input": 1000, "output": 250, "reasoning": 50, "cache": map[string]any{"read": 200, "write": 0}}}})
	}
}

// useFakeAgents installs both fake CLIs and returns the path of the invocation log.
func useFakeAgents(t *testing.T, a *App, mode string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("LOOPBEE_FAKE_MODE", mode)
	t.Setenv("LOOPBEE_FAKE_LOG", log)
	for _, n := range AgentNames() {
		agentOverride[n] = []string{os.Args[0], "-test.run=^TestFakeAgent$", "--", "@fake=" + n}
	}
	t.Cleanup(func() {
		for _, n := range AgentNames() {
			delete(agentOverride, n)
		}
	})
	return log
}

// The fake must know which CLI it impersonates; the variable is set per call through the Loop env.
func fakeCalls(t *testing.T, log string) []map[string]any {
	t.Helper()
	f, err := os.Open(log)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		json.Unmarshal(sc.Bytes(), &m)
		out = append(out, m)
	}
	return out
}

func TestBuildAgentArgs(t *testing.T) {
	codexCaps := []string{"headless", "last-message", "schema", "add-dir", "skip-git-check"}
	claudeCaps := []string{"headless", "schema", "add-dir", "tools", "settings", "permission-mode"}
	join := func(v []string) string { return strings.Join(v, " ") }
	cases := []struct {
		name, sandbox string
		caps          []string
		want, not     []string
	}{
		{"codex", "read-only", codexCaps, []string{"exec --json", "--sandbox read-only", "-C /w -"}, []string{"network_access"}},
		{"codex", "workspace-write", codexCaps, []string{"--sandbox workspace-write"}, []string{"network_access"}},
		{"codex", "workspace-network", codexCaps, []string{"--sandbox workspace-write -c sandbox_workspace_write.network_access=true"}, nil},
		{"codex", "agent-config", codexCaps, []string{"exec --json"}, []string{"--sandbox"}},
		{"codex", "workspace-write", []string{"headless"}, []string{"--sandbox workspace-write"}, []string{"--add-dir", "--output-last-message"}},
		{"claude", "read-only", claudeCaps, []string{"-p --output-format stream-json", "--permission-mode dontAsk --tools Read,Grep,Glob"}, []string{"acceptEdits"}},
		{"claude", "workspace-write", claudeCaps, []string{"--permission-mode acceptEdits", `"allowedDomains":[]`, `"failIfUnavailable":true`}, []string{`"*"`}},
		{"claude", "workspace-network", claudeCaps, []string{"--permission-mode acceptEdits", `"allowedDomains":["*"]`}, nil},
		{"claude", "agent-config", claudeCaps, []string{"-p --output-format stream-json"}, []string{"--permission-mode", "--settings"}},
	}
	for _, c := range cases {
		got := join(buildAgentArgs([]string{c.name}, c.caps, agentInvocation{Name: c.name, Dir: "/w", Sandbox: c.sandbox, AddDirs: []string{"/repo/.git"}, LastFile: "/r/last.md"}))
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s %s: %q missing %q", c.name, c.sandbox, got, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s %s: %q must not contain %q", c.name, c.sandbox, got, n)
			}
		}
	}
	if !sandboxWithin("workspace-network", "agent-config") || sandboxWithin("agent-config", "workspace-network") || sandboxWithin("bogus", "agent-config") {
		t.Fatal("permission ordering")
	}
}

func TestAgentFailureReason(t *testing.T) {
	claude := `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["Sandbox required but unavailable: bubblewrap (bwrap) not installed"]}`
	if got := agentFailure([]byte(claude)); !strings.Contains(got, "bubblewrap") {
		t.Fatal(got)
	}
	if got := agentFailure([]byte(`{"type":"turn.failed","error":{"message":"rate limited"}}`)); got != "rate limited" {
		t.Fatal(got)
	}
	// Real Claude Code 2.1.296 output when the subscription's session limit is reached.
	limit := `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"api_error":"usage_limit_reached","result":"You've hit your session limit · resets 10:20pm (Asia/Taipei)"}`
	if got := agentFailure([]byte(limit)); got != "You've hit your session limit · resets 10:20pm (Asia/Taipei)" {
		t.Fatal(got)
	}
}

func TestParseResultContract(t *testing.T) {
	r := parseResult("work done\n" + resultMarker + ` {"status":"done","summary":"ok","links":["https://x/pr/1","javascript:alert(1)"]}`)
	if r == nil || r.Status != "done" || len(r.Links) != 1 {
		t.Fatalf("%+v", r)
	}
	if parseResult(resultMarker+` {"status":"weird"}`) != nil || parseResult("no marker") != nil {
		t.Fatal("invalid results must be ignored")
	}
	// The last marker wins (the prompt itself quotes the format once).
	r = parseResult(resultMarker + ` {"status":"done"}` + "\n" + resultMarker + ` {"status":"no_action","summary":"x"}`)
	if r.Status != "no_action" {
		t.Fatal(r.Status)
	}
}

func agentLoop(t *testing.T, agent string) Loop {
	l := testLoop(t)
	l.Agent = agent
	l.Sandbox = "read-only"
	l.Env = []EnvVar{{Name: "LOOPBEE_FAKE_AGENT", Value: agent}}
	l.Steps = []Step{{Kind: "act", Name: "Sweep", Prompt: "Use /fix-prod-errors and stop before master"}}
	return l
}

func TestAgentStepOutcomesForBothCLIs(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, c := range []struct{ mode, status string }{{"done", "completed"}, {"no_action", "no_action"}, {"blocked", "blocked"}, {"plain", "completed"}, {"fail", "failed"}} {
			t.Run(agent+"/"+c.mode, func(t *testing.T) {
				a := testApp(t)
				log := useFakeAgents(t, a, c.mode)
				t.Setenv("LOOPBEE_FAKE_AGENT", agent)
				a.DetectAgents()
				if ag, _ := a.agentInfo(agent); ag.Status != "available" {
					t.Fatalf("fake %s not detected: %+v", agent, ag)
				}
				l, err := a.SaveLoop(agentLoop(t, agent))
				if err != nil {
					t.Fatal(err)
				}
				r, err := a.Start(l.ID)
				if err != nil {
					t.Fatal(err)
				}
				waitStatus(t, a, r.ID, c.status)
				r, _ = a.Run(r.ID)
				calls := fakeCalls(t, log)
				if len(calls) != 1 {
					t.Fatalf("calls: %v", calls)
				}
				prompt := calls[0]["prompt"].(string)
				if !strings.Contains(prompt, "/fix-prod-errors") || !strings.Contains(prompt, resultMarker) {
					t.Fatalf("prompt: %s", prompt)
				}
				switch c.mode {
				case "done":
					if r.Result == nil || r.Result.Summary != "Fixed 1 error, deployed to test" || len(r.Result.Links) != 1 {
						t.Fatalf("result %+v", r.Result)
					}
					if !slices.Contains(r.Artifacts, "round-01-step-01.report.md") || !strings.Contains(r.Report, "checkout-timeout") {
						t.Fatalf("report missing: %v %q", r.Artifacts, r.Report)
					}
					if r.Trace[0].Note != "Fixed 1 error, deployed to test" {
						t.Fatalf("trace note %q", r.Trace[0].Note)
					}
				case "blocked":
					if r.Error != "Cannot reach the log service" {
						t.Fatal(r.Error)
					}
				case "plain":
					if r.Result != nil || r.Report == "" {
						t.Fatalf("plain final message should be kept without a result: %+v", r)
					}
				}
			})
		}
	}
}

func TestGateWaitIsOutsideTheTimeBudgetAndExpires(t *testing.T) {
	oldWall, oldGate := wallUnit, gateUnit
	wallUnit, gateUnit = 100*time.Millisecond, 400*time.Millisecond
	t.Cleanup(func() { wallUnit, gateUnit = oldWall, oldGate })
	a := testApp(t)
	l := testLoop(t)
	l.WallMinutes = 3 // 300 ms of execution budget
	l.GateHours = 1   // 400 ms gate expiry
	l.Steps = []Step{{Kind: "gate", Name: "Review"}}
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "waiting")
	time.Sleep(350 * time.Millisecond) // longer than the execution budget
	if err := a.Decide(r.ID, true, "fine"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, a, r.ID, "completed")

	r2, _ := a.Start(l.ID)
	waitStatus(t, a, r2.ID, "waiting")
	time.Sleep(600 * time.Millisecond)
	got, _ := a.Run(r2.ID)
	if got.Status != "timed_out" || !strings.Contains(got.Error, "gate expired") || got.Trace[0].Status != "expired" {
		t.Fatalf("gate expiry: %+v", got)
	}
	if err := a.Decide(r2.ID, true, "late"); err == nil {
		t.Fatal("an expired gate must not accept decisions")
	}
}

func TestCommandStepEnvironmentAndFailure(t *testing.T) {
	a := testApp(t)
	t.Setenv("FAKE_SECRET_SOURCE", "s3cr3t")
	t.Setenv("LOOPBEE_HELPER", "env")
	l := testLoop(t)
	l.AllowedTools = []string{os.Args[0]}
	l.Env = []EnvVar{{Name: "PLAIN", Value: "visible"}, {Name: "DB_PASSWORD", Value: "env:FAKE_SECRET_SOURCE", Secret: true}}
	l.Steps = []Step{{Kind: "run", Name: "Sync", Argv: []string{os.Args[0], "-test.run=^TestHelperProcess$"}}}
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "completed")
	r, _ = a.Run(r.ID)
	b, _ := a.ReadArtifact(r.ID, r.Artifacts[0])
	if !strings.Contains(string(b), "PLAIN=visible DB_PASSWORD=s3cr3t") {
		t.Fatalf("env not injected: %s", b)
	}
	// A failing run step fails the Run instead of starting another round.
	t.Setenv("LOOPBEE_HELPER", "failure")
	l.MaxRounds = 3
	l, _ = a.SaveLoop(l)
	r, _ = a.Start(l.ID)
	waitStatus(t, a, r.ID, "failed")
	r, _ = a.Run(r.ID)
	if r.Round != 1 || !strings.Contains(r.Error, "Sync") {
		t.Fatalf("run step failure: %+v", r)
	}
	// A secret without a value blocks the Run before anything executes.
	l.Env = []EnvVar{{Name: "DB_PASSWORD", Secret: true}}
	l, err = a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ = a.Start(l.ID)
	waitStatus(t, a, r.ID, "blocked")
}

func TestWorktreeWorkspaceLeavesTheCheckoutAlone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0600)
	git("add", ".")
	git("commit", "-qm", "init")
	os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("uncommitted"), 0600) // the developer's work in progress
	a := testApp(t)
	t.Setenv("LOOPBEE_HELPER", "pwd")
	l := testLoop(t)
	l.Directory = repo
	l.Workspace = "worktree"
	l.RequireClean = true
	l.AllowedTools = []string{os.Args[0]}
	l.Steps = []Step{{Kind: "run", Name: "Where", Argv: []string{os.Args[0], "-test.run=^TestHelperProcess$"}}}
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "completed")
	r, _ = a.Run(r.ID)
	want, _ := filepath.EvalSymlinks(filepath.Join(a.Dir, "worktrees", l.ID))
	b, _ := a.ReadArtifact(r.ID, r.Artifacts[0])
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(b)))
	if got != want || r.Workdir == repo {
		t.Fatalf("ran in %q, want worktree %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(want, "dirty.txt")); err == nil {
		t.Fatal("worktree must start from the committed base, not the dirty checkout")
	}
	if _, err := os.Stat(filepath.Join(repo, "dirty.txt")); err != nil {
		t.Fatal("developer checkout was modified")
	}
	// A second Run recreates the worktree cleanly.
	r, _ = a.Start(l.ID)
	waitStatus(t, a, r.ID, "completed")
}

func TestAuthorProposalIsNormalisedDraft(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			a := testApp(t)
			log := useFakeAgents(t, a, "author")
			t.Setenv("LOOPBEE_FAKE_AGENT", agent)
			a.DetectAgents()
			c := a.Settings()
			c.MaxSandbox = "workspace-network"
			if err := a.SaveSettings(c); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			reply, err := a.Author(t.Context(), AuthorRequest{Agent: agent, Directory: dir, Messages: []ChatMessage{{Role: "user", Text: "Nightly: fix prod errors with our skill, stop before master"}}})
			if err != nil {
				t.Fatal(err)
			}
			l := reply.Loop
			if l.Name != "Fix production errors" || l.Enabled || !l.Private || l.Directory != dir || l.Agent != agent || l.ID != "" {
				t.Fatalf("draft not normalised: %+v", l)
			}
			if len(l.Steps) != 2 || l.Steps[0].Argv != nil || l.Steps[1].Kind != "gate" || l.GateHours != 48 || l.Workspace != "worktree" || l.RequireClean {
				t.Fatalf("steps/workspace: %+v", l)
			}
			if len(l.Env) != 1 || l.Env[0].Value != "" {
				t.Fatalf("agent-supplied env values must be dropped: %+v", l.Env)
			}
			if reply.Problem != "" || len(reply.Questions) != 1 {
				t.Fatalf("problem %q questions %v", reply.Problem, reply.Questions)
			}
			call := fakeCalls(t, log)[0]
			args := fmt.Sprint(call["args"])
			if agent == "codex" && (!strings.Contains(args, "--sandbox read-only") || !strings.Contains(args, "--output-schema")) {
				t.Fatalf("codex authoring must be read-only with a schema: %s", args)
			}
			if agent == "claude" && (!strings.Contains(args, "--tools Read,Grep,Glob") || !strings.Contains(args, "--json-schema")) {
				t.Fatalf("claude authoring must be read-only with a schema: %s", args)
			}
			cwd, _ := filepath.EvalSymlinks(call["cwd"].(string))
			wantDir, _ := filepath.EvalSymlinks(dir)
			if cwd != wantDir {
				t.Fatalf("authoring ran in %s, want project directory %s", cwd, wantDir)
			}
			// The proposal saves once the operator fills the missing secret.
			l.Env[0].Value = "env:HOME"
			if _, err := a.SaveLoop(l); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunNotificationContentAndRules(t *testing.T) {
	r := Run{ID: "0123456789abcdef01234567", Status: "completed", Definition: Loop{Name: "Fix prod"}, Report: "Full report body\n" + resultMarker + ` {"status":"done"}`,
		Result: &AgentResult{Status: "done", Summary: "Fixed 1 error", Links: []string{"https://example.com/pr/7"}}}
	text := runNotification(r, "zh-TW", true)
	for _, want := range []string{"Fix prod · 完成", "Fixed 1 error", "https://example.com/pr/7", "Full report body", "run 0123456789"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q missing %q", text, want)
		}
	}
	if strings.Contains(text, resultMarker) {
		t.Fatal("result marker leaked into the notification")
	}
	a := testApp(t)
	c := a.Settings()
	c.Channels = []Channel{{Kind: "desktop", Enabled: true}}
	if err := a.SaveSettings(c); err != nil {
		t.Fatal(err)
	}
	count := func() int { d, _ := list[Delivery](a, "delivery"); return len(d) }
	a.notifyRun(Run{ID: r.ID, Status: "no_action", Definition: Loop{Name: "x"}})
	if count() != 0 {
		t.Fatal("no_action is silent by default")
	}
	a.notifyRun(Run{ID: r.ID, Status: "no_action", Definition: Loop{Name: "x", Notify: NotifyRule{Events: []string{"no_action"}}}})
	if count() != 1 {
		t.Fatal("subscribed no_action must notify")
	}
	a.notifyRun(Run{ID: r.ID, Status: "failed", Definition: Loop{Name: "x", Notify: NotifyRule{Channels: []string{"bark"}}}})
	if count() != 1 {
		t.Fatal("channel filter ignored")
	}
}

func TestLoginShellPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login shell PATH is not used on Windows")
	}
	if got := mergePath("/usr/bin:/bin", "/home/u/.nvm/bin:/usr/bin"); got != "/home/u/.nvm/bin:/usr/bin:/bin" {
		t.Fatal(got)
	}
	dir := t.TempDir()
	shell := filepath.Join(dir, "fakesh")
	os.WriteFile(shell, []byte("#!/bin/sh\necho 'motd noise'\nprintf '\\n__LOOPBEE_PATH__/opt/agents/bin:/usr/bin\\n'\n"), 0700)
	t.Setenv("SHELL", shell)
	t.Setenv("PATH", "/usr/bin:/bin")
	adoptLoginPath()
	if os.Getenv("PATH") != "/opt/agents/bin:/usr/bin:/bin" {
		t.Fatal(os.Getenv("PATH"))
	}
}

// A run step marked continueOnError records the failure and hands its output to the agent step.
func TestCommandOutputReachesTheAgentStep(t *testing.T) {
	a := testApp(t)
	log := useFakeAgents(t, a, "done")
	t.Setenv("LOOPBEE_FAKE_AGENT", "claude")
	t.Setenv("LOOPBEE_HELPER", "failure")
	a.DetectAgents()
	l := agentLoop(t, "claude")
	l.AllowedTools = append(l.AllowedTools, os.Args[0])
	l.Steps = append([]Step{{Kind: "run", Name: "Tests", Argv: []string{os.Args[0], "-test.run=^TestHelperProcess$"}, ContinueOnError: true}}, l.Steps...)
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "completed")
	r, _ = a.Run(r.ID)
	if r.Trace[0].Status != "failed" || r.Trace[1].Status != "ok" {
		t.Fatalf("trace: %+v", r.Trace)
	}
	calls := fakeCalls(t, log)
	prompt := calls[0]["prompt"].(string)
	if !strings.Contains(prompt, "Output of the earlier steps") || !strings.Contains(prompt, "### Tests") || !strings.Contains(prompt, "evidence") || !strings.Contains(prompt, "failed: exit status 1") {
		t.Fatalf("earlier output not passed on: %s", prompt)
	}
	// continueOnError is only valid on command steps.
	l.Steps[1].ContinueOnError = true
	if _, err := a.SaveLoop(l); err == nil {
		t.Fatal("continueOnError accepted on an agent step")
	}
}

func TestAuthorPromptKnowsTheAgentAndTimezone(t *testing.T) {
	req := AuthorRequest{Messages: []ChatMessage{{Role: "user", Text: "daily"}}}
	p := authorPrompt(req, "/x", "zh-TW", "Asia/Taipei", "claude", "workspace-write", Loop{})
	if !strings.Contains(p, "cannot run any shell") || !strings.Contains(p, "timezone Asia/Taipei") || !strings.Contains(p, "continueOnError") || !strings.Contains(p, `allows at most "workspace-write"`) || !strings.Contains(p, "requires network permission") || !strings.Contains(p, "commits the agent's changes") {
		t.Fatal(p)
	}
	if p := authorPrompt(req, "/x", "en", "UTC", "codex", "read-only", Loop{}); !strings.Contains(p, "Codex can run shell commands inside a read-only sandbox") {
		t.Fatal(p)
	}
}

// With a branch, Loopbee creates it in the worktree and commits the agent's changes itself.
func TestBranchWorkspaceCommitsForTheAgent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "master")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0600)
	git("add", ".")
	git("commit", "-qm", "init")
	head := git("rev-parse", "HEAD")
	os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("uncommitted"), 0600)
	a := testApp(t)
	log := useFakeAgents(t, a, "edit")
	t.Setenv("LOOPBEE_FAKE_AGENT", "codex")
	a.DetectAgents()
	l := agentLoop(t, "codex")
	l.Name = "Fix tests"
	l.Directory = repo
	l.Workspace = "worktree"
	l.Branch = "loopbee/fix-{date}"
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "completed")
	r, _ = a.Run(r.ID)
	want := "loopbee/fix-" + time.Now().Format("20060102")
	if r.Branch != want || len(r.Commits) != 1 {
		t.Fatalf("branch %q commits %v", r.Branch, r.Commits)
	}
	if got := git("show", want+":fixed.txt"); got != "fixed by the agent" {
		t.Fatalf("commit content: %q", got)
	}
	if subject := git("log", "-1", "--format=%s", want); !strings.HasPrefix(subject, "Fix tests: Fixed 1 error") {
		t.Fatalf("commit subject: %q", subject)
	}
	if git("rev-parse", "HEAD") != head || git("rev-parse", "--abbrev-ref", "HEAD") != "master" {
		t.Fatal("developer checkout moved")
	}
	if _, err := os.Stat(filepath.Join(repo, "dirty.txt")); err != nil {
		t.Fatal("developer's uncommitted work disappeared")
	}
	prompt := fakeCalls(t, log)[0]["prompt"].(string)
	if !strings.Contains(prompt, "You are on branch "+want) || !strings.Contains(prompt, "do not commit") {
		t.Fatalf("prompt: %s", prompt)
	}
	// The same branch name again is refused instead of overwriting unmerged work.
	r, _ = a.Start(l.ID)
	waitStatus(t, a, r.ID, "blocked")
	if r, _ = a.Run(r.ID); !strings.Contains(r.Error, "already exists") {
		t.Fatalf("second run: %s", r.Error)
	}
	// Shared branches and branches outside a worktree are rejected.
	for _, bad := range []Loop{{Workspace: "worktree", Branch: "master"}, {Workspace: "direct", Branch: "loopbee/x"}, {Workspace: "worktree", Branch: "a..b"}} {
		c := l
		c.Workspace, c.Branch = bad.Workspace, bad.Branch
		if Validate(c) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func fakeTimezone() string {
	if tz, ok := os.LookupEnv("LOOPBEE_FAKE_TZ"); ok {
		return tz
	}
	return "Asia/Taipei"
}

// Lesson: drafts defaulted to UTC. A draft without a time zone takes the user's (sent by the UI).
func TestAuthorUsesTheUserTimezone(t *testing.T) {
	a := testApp(t)
	log := useFakeAgents(t, a, "author")
	t.Setenv("LOOPBEE_FAKE_AGENT", "claude")
	t.Setenv("LOOPBEE_FAKE_TZ", "")
	a.DetectAgents()
	reply, err := a.Author(t.Context(), AuthorRequest{Agent: "claude", Timezone: "America/New_York", Messages: []ChatMessage{{Role: "user", Text: "every morning"}}})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Loop.Timezone != "America/New_York" {
		t.Fatalf("timezone %q", reply.Loop.Timezone)
	}
	if prompt := fakeCalls(t, log)[0]["prompt"].(string); !strings.Contains(prompt, "timezone America/New_York") {
		t.Fatal("the designer was not told the user's time zone")
	}
	// An invalid zone from the client is ignored rather than producing an unsavable draft.
	reply, err = a.Author(t.Context(), AuthorRequest{Agent: "claude", Timezone: "Mars/Olympus", Messages: []ChatMessage{{Role: "user", Text: "every morning"}}})
	if err != nil || reply.Loop.Timezone != a.Settings().DigestTimezone {
		t.Fatalf("invalid zone: %v %q", err, reply.Loop.Timezone)
	}
}

// Lesson: a draft above the device ceiling must say so instead of failing later at save time.
func TestAuthorFlagsPermissionAboveTheDeviceLimit(t *testing.T) {
	a := testApp(t)
	useFakeAgents(t, a, "author") // proposes workspace-network; the default ceiling is read-only
	t.Setenv("LOOPBEE_FAKE_AGENT", "codex")
	a.DetectAgents()
	reply, err := a.Author(t.Context(), AuthorRequest{Agent: "codex", Messages: []ChatMessage{{Role: "user", Text: "fix and deploy"}}})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Problem != "permission exceeds this device's limit" {
		t.Fatalf("problem %q", reply.Problem)
	}
}

// Lesson: a subscription limit showed up as raw JSON in the chat.
func TestAuthorReportsAgentUsageLimit(t *testing.T) {
	a := testApp(t)
	useFakeAgents(t, a, "limit")
	t.Setenv("LOOPBEE_FAKE_AGENT", "claude")
	a.DetectAgents()
	_, err := a.Author(t.Context(), AuthorRequest{Agent: "claude", Messages: []ChatMessage{{Role: "user", Text: "anything"}}})
	if err == nil || err.Error() != "agent failed: You've hit your session limit · resets 10:20pm (Asia/Taipei)" {
		t.Fatalf("error: %v", err)
	}
}

// A polling command can end the Run as "nothing to do" (exit 78) before any agent or gate runs, and
// every step sees the Run's id, folder and branch.
func TestCommandStepNothingToDoAndRunEnvironment(t *testing.T) {
	a := testApp(t)
	log := useFakeAgents(t, a, "done")
	t.Setenv("LOOPBEE_FAKE_AGENT", "codex")
	a.DetectAgents()
	l := agentLoop(t, "codex")
	l.AllowedTools = []string{os.Args[0]}
	poll := Step{Kind: "run", Name: "Find issues", Argv: []string{os.Args[0], "-test.run=^TestHelperProcess$"}}
	l.Steps = append([]Step{poll}, append(l.Steps, Step{Kind: "gate", Name: "Review"})...)
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOPBEE_HELPER", "nothing")
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "no_action")
	r, _ = a.Run(r.ID)
	if r.Result == nil || r.Result.Summary != "No open issues labelled agent-ready" || len(r.Trace) != 1 || len(fakeCalls(t, log)) != 0 {
		t.Fatalf("nothing to do: %+v calls=%d", r, len(fakeCalls(t, log)))
	}
	t.Setenv("LOOPBEE_HELPER", "runenv")
	r, _ = a.Start(l.ID)
	waitStatus(t, a, r.ID, "waiting")
	r, _ = a.Run(r.ID)
	b, _ := a.ReadArtifact(r.ID, r.Artifacts[0])
	if want := fmt.Sprintf("run=%s dir=%s branch=\"\"", r.ID, r.ID); !strings.Contains(string(b), want) {
		t.Fatalf("run environment: %s, want %s", b, want)
	}
	a.Cancel(r.ID)
}

// A failed verification is shown to the agent in the next round; otherwise it retries blind.
func TestVerifyFailureReachesTheNextRound(t *testing.T) {
	a := testApp(t)
	log := useFakeAgents(t, a, "done")
	t.Setenv("LOOPBEE_FAKE_AGENT", "claude")
	t.Setenv("LOOPBEE_HELPER", "failure")
	a.DetectAgents()
	l := agentLoop(t, "claude")
	l.MaxRounds, l.StallRounds = 2, 3
	l.AllowedTools = []string{os.Args[0]}
	l.Steps = append(l.Steps, Step{Kind: "verify", Name: "Go tests", Argv: []string{os.Args[0], "-test.run=^TestHelperProcess$"}})
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "budget_exhausted")
	calls := fakeCalls(t, log)
	if len(calls) != 2 {
		t.Fatalf("agent calls: %d", len(calls))
	}
	if first := calls[0]["prompt"].(string); strings.Contains(first, "previous round's verification failed") {
		t.Fatal("round 1 has no previous failure")
	}
	second := calls[1]["prompt"].(string)
	if !strings.Contains(second, "previous round's verification failed") || !strings.Contains(second, "### Go tests") || !strings.Contains(second, "evidence") {
		t.Fatalf("round 2 prompt: %s", second)
	}
}

// A prompt-injected agent must not get code executed outside its sandbox through Loopbee: not through scripts that
// later command steps run, not through Git hooks or a redirected .git file, and credentials in its change stop the
// Run before review. The reviewer gets the committed diff.
func TestHostileAgentCannotEscapeThroughLoopbee(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	setup := func(t *testing.T, mode string) (*App, Loop, string) {
		repo := t.TempDir()
		git := func(args ...string) {
			cmd := exec.Command("git", args...)
			cmd.Dir = repo
			cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %s", args, out)
			}
		}
		git("init", "-q", "-b", "master")
		os.WriteFile(filepath.Join(repo, "tool.txt"), []byte("trusted\n"), 0600)
		os.MkdirAll(filepath.Join(repo, ".husky"), 0700)
		os.WriteFile(filepath.Join(repo, ".husky", "pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0700)
		git("add", ".")
		git("commit", "-qm", "init")
		git("config", "core.hooksPath", ".husky") // like a repository using husky
		a := testApp(t)
		log := useFakeAgents(t, a, mode)
		t.Setenv("LOOPBEE_FAKE_AGENT", "claude")
		t.Setenv("LOOPBEE_HELPER", "success")
		a.DetectAgents()
		l := agentLoop(t, "claude")
		l.Directory, l.Workspace, l.Branch = repo, "worktree", "loopbee/fix-{run}"
		l.AllowedTools = []string{os.Args[0]}
		l.Steps = append(l.Steps, Step{Kind: "run", Name: "Use the tool", Argv: []string{os.Args[0], "-test.run=^TestHelperProcess$", "tool.txt"}})
		l, err := a.SaveLoop(l)
		if err != nil {
			t.Fatal(err)
		}
		return a, l, log
	}
	t.Run("ordinary change: committed, diff kept, no .git access for the agent", func(t *testing.T) {
		a, l, log := setup(t, "edit")
		r, _ := a.Start(l.ID)
		waitStatus(t, a, r.ID, "completed")
		r, _ = a.Run(r.ID)
		b, _ := a.ReadArtifact(r.ID, "round-01-step-01.diff")
		if !strings.Contains(string(b), "+fixed by the agent") {
			t.Fatalf("diff artifact: %q", b)
		}
		if args := fmt.Sprint(fakeCalls(t, log)[0]["args"]); strings.Contains(args, "--add-dir") {
			t.Fatalf("agent got extra writable directories: %s", args)
		}
	})
	t.Run("changed script is not executed", func(t *testing.T) {
		a, l, _ := setup(t, "tamper")
		r, _ := a.Start(l.ID)
		waitStatus(t, a, r.ID, "blocked")
		if r, _ = a.Run(r.ID); !strings.Contains(r.Error, "would run tool.txt, which the agent changed") {
			t.Fatal(r.Error)
		}
	})
	// After a person approved the change at a gate, later commands may run it (a merged migration, a deploy script):
	// the sync Loop “merge → review → migrate → deploy” needs that. Before the gate it stays refused (above).
	t.Run("a reviewed change may run after the gate", func(t *testing.T) {
		a, l, _ := setup(t, "tamper")
		l.Steps = []Step{l.Steps[0], {Kind: "gate", Name: "Review"}, l.Steps[1]}
		l, err := a.SaveLoop(l)
		if err != nil {
			t.Fatal(err)
		}
		r, _ := a.Start(l.ID)
		waitStatus(t, a, r.ID, "waiting")
		if err := a.Decide(r.ID, true, "checked tool.txt"); err != nil {
			t.Fatal(err)
		}
		waitStatus(t, a, r.ID, "completed")
	})
	t.Run("hooks written by the agent never run", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("shell hooks")
		}
		marker := filepath.Join(t.TempDir(), "pwned")
		t.Setenv("LOOPBEE_FAKE_MARKER", marker)
		a, l, _ := setup(t, "hook")
		r, _ := a.Start(l.ID)
		waitStatus(t, a, r.ID, "completed") // .husky changed, tool.txt did not, so the command step may run
		r, _ = a.Run(r.ID)
		if len(r.Commits) != 1 {
			t.Fatalf("commit: %+v", r)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("a hook written by the agent was executed by Loopbee")
		}
	})
	t.Run("redirected .git file stops Loopbee's Git", func(t *testing.T) {
		a, l, _ := setup(t, "gitfile")
		r, _ := a.Start(l.ID)
		waitStatus(t, a, r.ID, "failed")
		if r, _ = a.Run(r.ID); !strings.Contains(r.Error, ".git file was changed") {
			t.Fatal(r.Error)
		}
	})
	t.Run("credentials in the change stop the run before review", func(t *testing.T) {
		a, l, _ := setup(t, "secret")
		r, _ := a.Start(l.ID)
		waitStatus(t, a, r.ID, "failed")
		r, _ = a.Run(r.ID)
		if !strings.Contains(r.Error, "looks like a credential (config.txt: ghp_a1B2a1B2…)") || !strings.Contains(r.Error, "nothing was pushed") {
			t.Fatal(r.Error)
		}
	})
}

func TestSecretsInPatchIgnoresOrdinaryCode(t *testing.T) {
	patch := "+++ b/a_test.go\n+\temail := \"t@example.invalid\"\n+\tpassword := os.Getenv(\"DB_PASSWORD\")\n-old ghp_" + strings.Repeat("x", 36) + "\n+++ b/key.pem\n+-----BEGIN OPENSSH PRIVATE KEY-----\n"
	if got := secretsInPatch([]byte(patch)); len(got) != 1 || !strings.HasPrefix(got[0], "key.pem: -----BEGIN") {
		t.Fatalf("%v", got)
	}
}

// The pull request a command step opens must reach the notification (it is what the reviewer acts on).
func TestCommandStepResultReachesTheNotification(t *testing.T) {
	a := testApp(t)
	useFakeAgents(t, a, "done")
	t.Setenv("LOOPBEE_FAKE_AGENT", "codex")
	t.Setenv("LOOPBEE_HELPER", "prlink")
	a.DetectAgents()
	l := agentLoop(t, "codex")
	l.AllowedTools = []string{os.Args[0]}
	l.Steps = append(l.Steps, Step{Kind: "run", Name: "Open pull request", Argv: []string{os.Args[0], "-test.run=^TestHelperProcess$"}})
	l, err := a.SaveLoop(l)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Start(l.ID)
	waitStatus(t, a, r.ID, "completed")
	r, _ = a.Run(r.ID)
	if r.Result == nil || r.Result.Status != "done" || r.Result.Summary != "Fixed 1 error, deployed to test\nPull request #13 is ready to merge" {
		t.Fatalf("result: %+v", r.Result)
	}
	text := runNotification(r, "zh-TW", false)
	for _, want := range []string{"Pull request #13 is ready to merge", "https://github.com/acme/app/pull/13", "https://example.com/pr/7"} {
		if !strings.Contains(text, want) {
			t.Fatalf("notification lacks %q:\n%s", want, text)
		}
	}
}
