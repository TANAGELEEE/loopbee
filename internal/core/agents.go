package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Agent is a locally installed agent CLI that Loopbee can drive headlessly.
type Agent struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Version string   `json:"version"`
	Status  string   `json:"status"` // available, unverified, unavailable
	Caps    []string `json:"caps,omitempty"`
}

// Permission levels, lowest to highest. A Loop may not exceed the device ceiling (Settings.MaxSandbox).
//
//	read-only          agent may read the working directory only
//	workspace-write    agent may edit files and run commands inside the working directory, no network
//	workspace-network  as workspace-write, plus outbound network (git push, deploy, databases, APIs)
//	agent-config       Loopbee adds no sandbox flags; the agent's own config/profile/settings decide
var sandboxRank = map[string]int{"read-only": 0, "workspace-write": 1, "workspace-network": 2, "agent-config": 3}

func validSandbox(s string) bool { _, ok := sandboxRank[s]; return ok }
func sandboxWithin(level, ceiling string) bool {
	return validSandbox(level) && validSandbox(ceiling) && sandboxRank[level] <= sandboxRank[ceiling]
}

// agentOverride lets tests replace an agent executable with a fake (argv prefix).
var agentOverride = map[string][]string{}

func agentCommand(name string) ([]string, error) {
	if argv, ok := agentOverride[name]; ok {
		return append([]string{}, argv...), nil
	}
	// LOOPBEE_CODEX_BIN / LOOPBEE_CLAUDE_BIN point at a specific executable (or a .js/.mjs entry point).
	if custom := os.Getenv("LOOPBEE_" + strings.ToUpper(name) + "_BIN"); custom != "" {
		if strings.HasSuffix(custom, ".js") || strings.HasSuffix(custom, ".mjs") {
			node, err := exec.LookPath("node")
			if err != nil {
				return nil, err
			}
			return []string{node, custom}, nil
		}
		return []string{custom}, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" && (strings.HasSuffix(p, ".cmd") || strings.HasSuffix(p, ".ps1")) {
		scripts := map[string]string{
			"codex":  filepath.Join(filepath.Dir(p), "node_modules", "@openai", "codex", "bin", "codex.js"),
			"claude": filepath.Join(filepath.Dir(p), "node_modules", "@anthropic-ai", "claude-code", "cli.js"),
		}
		script, ok := scripts[name]
		if !ok {
			return nil, errors.New("agent requires a native executable")
		}
		if _, err := os.Stat(script); err != nil {
			return nil, err
		}
		node, err := exec.LookPath("node")
		if err != nil {
			return nil, err
		}
		return []string{node, script}, nil
	}
	return []string{p}, nil
}

// DetectAgents probes each supported CLI for its version and headless capabilities.
func (a *App) DetectAgents() {
	out := []Agent{}
	for _, name := range []string{"codex", "claude"} {
		ag := Agent{Name: name, Status: "unavailable"}
		argv, err := agentCommand(name)
		if err == nil {
			ag.Path = argv[len(argv)-1]
			if _, ok := agentOverride[name]; ok {
				ag.Path = argv[0]
			}
			ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
			b, e := runCommand(ctx, "", append(argv, "--version"), nil)
			cancel()
			if e == nil {
				ag.Version = strings.TrimSpace(string(b))
				ag.Status = "unverified"
				helpArgs := []string{"--help"}
				if name == "codex" {
					helpArgs = []string{"exec", "--help"}
				}
				ctx, cancel = context.WithTimeout(a.ctx, 8*time.Second)
				help, e := runCommand(ctx, "", append(argv, helpArgs...), nil)
				cancel()
				if e == nil {
					ag.Caps = capabilities(name, help)
					if hasCap(ag.Caps, "headless") {
						ag.Status = "available"
					}
				}
			}
		}
		out = append(out, ag)
	}
	gctx, gcancel := context.WithTimeout(a.ctx, 8*time.Second)
	github := detectGitHub(gctx)
	gcancel()
	a.mu.Lock()
	a.agents = out
	a.github = github
	if a.config.DefaultAgent == "" {
		for _, ag := range out {
			if ag.Status == "available" {
				a.config.DefaultAgent = ag.Name
				a.put("settings", "general", a.config)
				break
			}
		}
	}
	a.mu.Unlock()
}

// capabilities reads the CLI help text; flags Loopbee relies on are only used when present.
func capabilities(name string, help []byte) []string {
	has := func(s string) bool { return bytes.Contains(help, []byte(s)) }
	caps := []string{}
	add := func(c string, ok bool) {
		if ok {
			caps = append(caps, c)
		}
	}
	switch name {
	case "codex":
		add("headless", has("--json") && has("--sandbox"))
		add("last-message", has("--output-last-message"))
		add("schema", has("--output-schema"))
		add("add-dir", has("--add-dir"))
		add("skip-git-check", has("--skip-git-repo-check"))
	case "claude":
		add("headless", has("--print") && has("--output-format") && has("stream-json"))
		add("schema", has("--json-schema"))
		add("add-dir", has("--add-dir"))
		add("tools", has("--tools"))
		add("settings", has("--settings"))
		add("permission-mode", has("--permission-mode"))
	}
	return caps
}
func hasCap(caps []string, c string) bool {
	for _, x := range caps {
		if x == c {
			return true
		}
	}
	return false
}
func (a *App) agentInfo(name string) (Agent, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ag := range a.agents {
		if ag.Name == name {
			return ag, true
		}
	}
	return Agent{}, false
}

// agentInvocation is everything needed to run one headless agent turn.
type agentInvocation struct {
	Name     string
	Dir      string   // working directory
	Sandbox  string   // permission level
	AddDirs  []string // extra writable directories (e.g. the git dir of a worktree)
	LastFile string   // codex: file for the final message
	Schema   string   // optional JSON schema (file path for codex, inline JSON for claude)
}

// claudeSandboxSettings maps Loopbee permission levels to Claude Code's OS sandbox for Bash.
func claudeSandboxSettings(network bool) string {
	domains := []string{}
	if network {
		domains = []string{"*"}
	}
	b, _ := json.Marshal(map[string]any{"sandbox": map[string]any{
		"enabled": true, "autoAllowBashIfSandboxed": true, "allowUnsandboxedCommands": false, "failIfUnavailable": true,
		"network": map[string]any{"allowedDomains": domains},
	}})
	return string(b)
}

// buildAgentArgs returns the argv for one turn. The prompt is always passed on stdin.
func buildAgentArgs(base []string, caps []string, inv agentInvocation) []string {
	argv := append([]string{}, base...)
	switch inv.Name {
	case "codex":
		argv = append(argv, "exec", "--json", "--color", "never")
		switch inv.Sandbox {
		case "read-only", "workspace-write":
			argv = append(argv, "--sandbox", inv.Sandbox)
		case "workspace-network":
			argv = append(argv, "--sandbox", "workspace-write", "-c", "sandbox_workspace_write.network_access=true")
		}
		if hasCap(caps, "add-dir") {
			for _, d := range inv.AddDirs {
				argv = append(argv, "--add-dir", d)
			}
		}
		if inv.LastFile != "" && hasCap(caps, "last-message") {
			argv = append(argv, "--output-last-message", inv.LastFile)
		}
		if inv.Schema != "" && hasCap(caps, "schema") {
			argv = append(argv, "--output-schema", inv.Schema)
		}
		if hasCap(caps, "skip-git-check") {
			argv = append(argv, "--skip-git-repo-check")
		}
		argv = append(argv, "-C", inv.Dir, "-")
	case "claude":
		argv = append(argv, "-p", "--output-format", "stream-json", "--verbose")
		switch inv.Sandbox {
		case "read-only":
			argv = append(argv, "--permission-mode", "dontAsk", "--tools", "Read,Grep,Glob")
		case "workspace-write":
			argv = append(argv, "--permission-mode", "acceptEdits", "--settings", claudeSandboxSettings(false))
		case "workspace-network":
			argv = append(argv, "--permission-mode", "acceptEdits", "--settings", claudeSandboxSettings(true))
		}
		if hasCap(caps, "add-dir") {
			for _, d := range inv.AddDirs {
				argv = append(argv, "--add-dir", d)
			}
		}
		if inv.Schema != "" && hasCap(caps, "schema") {
			argv = append(argv, "--json-schema", inv.Schema)
		}
	}
	return argv
}

// classifyAgentEvent labels one stdout line. Unknown or non-JSON lines are kept as agent.raw.
func classifyAgentEvent(line string) (string, bool) {
	var event struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		IsError bool   `json:"is_error"`
	}
	if json.Unmarshal([]byte(line), &event) == nil {
		switch event.Type {
		// Codex exec --json
		case "thread.started", "turn.started", "turn.completed", "turn.failed", "item.started", "item.updated", "item.completed", "error":
			return "agent." + event.Type, event.Type == "turn.failed"
		// Claude Code -p --output-format stream-json
		case "system", "assistant", "user", "stream_event":
			return "agent." + event.Type, false
		case "result":
			return "agent.result", event.IsError || (event.Subtype != "" && event.Subtype != "success")
		}
	}
	return "agent.raw", false
}

// finalMessage extracts the agent's last assistant message from its event stream.
func finalMessage(name string, output []byte) (string, map[string]any) {
	last := ""
	var structured map[string]any
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev struct {
			Type       string         `json:"type"`
			Result     string         `json:"result"`
			Structured map[string]any `json:"structured_output"`
			Item       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch {
		case name == "codex" && ev.Type == "item.completed" && ev.Item.Type == "agent_message":
			last = ev.Item.Text
		case name == "claude" && ev.Type == "assistant":
			var parts []string
			for _, c := range ev.Message.Content {
				if c.Type == "text" && c.Text != "" {
					parts = append(parts, c.Text)
				}
			}
			if len(parts) > 0 {
				last = strings.Join(parts, "\n")
			}
		case name == "claude" && ev.Type == "result":
			if ev.Result != "" {
				last = ev.Result
			}
			if ev.Structured != nil {
				structured = ev.Structured
			}
		}
	}
	return last, structured
}

// Result contract appended to every agent step so Loopbee can tell done from no-op or blocked.
const resultMarker = "LOOPBEE_RESULT"

func resultInstructions(lang string) string {
	return "\n\n---\nLoopbee runs this step unattended. Report in " + languageName(lang) + ". " +
		"When you finish, end your final message with exactly one line:\n" + resultMarker +
		` {"status":"done|no_action|blocked|needs_approval","summary":"one or two sentences","links":["URLs of PRs, deployments or reports"]}` +
		"\nUse no_action when there was nothing to do, blocked when you could not proceed (missing access, failing preconditions), and needs_approval when a person must decide before anything else happens. Never include secrets in the summary."
}
func languageName(l string) string {
	switch l {
	case "zh-TW":
		return "Traditional Chinese (zh-TW)"
	case "zh-CN":
		return "Simplified Chinese (zh-CN)"
	}
	return "English"
}

// AgentResult is the structured end-of-step report from the result contract.
type AgentResult struct {
	Status  string   `json:"status"`
	Summary string   `json:"summary"`
	Links   []string `json:"links,omitempty"`
}

func parseResult(text string) *AgentResult {
	idx := strings.LastIndex(text, resultMarker)
	if idx < 0 {
		return nil
	}
	rest := strings.TrimSpace(text[idx+len(resultMarker):])
	if i := strings.IndexByte(rest, '{'); i >= 0 {
		rest = rest[i:]
	}
	var r AgentResult
	dec := json.NewDecoder(strings.NewReader(rest))
	if dec.Decode(&r) != nil {
		return nil
	}
	switch r.Status {
	case "done", "no_action", "blocked", "needs_approval":
	default:
		return nil
	}
	if len(r.Summary) > 1000 {
		r.Summary = r.Summary[:1000]
	}
	links := []string{}
	for _, l := range r.Links {
		if strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") {
			links = append(links, l)
		}
	}
	r.Links = links
	return &r
}

// agentFailure extracts a readable reason from a failed agent turn (Claude result errors, Codex turn.failed).
func agentFailure(output []byte) string {
	reason := ""
	for _, line := range strings.Split(string(output), "\n") {
		var ev struct {
			Type    string   `json:"type"`
			Errors  []string `json:"errors"`
			Result  string   `json:"result"`
			IsError bool     `json:"is_error"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &ev) != nil {
			continue
		}
		switch {
		case len(ev.Errors) > 0:
			reason = ev.Errors[0]
		case ev.Type == "result" && ev.IsError && ev.Result != "":
			reason = ev.Result // e.g. Claude Code "You've hit your session limit · resets 10:20pm"
		case ev.Type == "turn.failed" && ev.Error.Message != "":
			reason = ev.Error.Message
		case ev.Type == "error" && ev.Message != "":
			reason = ev.Message
		}
	}
	if len(reason) > 500 {
		reason = reason[:500] + "…"
	}
	return reason
}
