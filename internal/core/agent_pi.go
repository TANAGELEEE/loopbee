package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// pi (pi coding agent): `pi --mode json`. pi has no sandbox and no permission prompts by design: every enabled tool
// runs with the user's own rights. loopbee can therefore only promise read-only, by giving it the read tools alone;
// for anything more the user's own configuration decides (agent-config).
func init() {
	registerAdapter(&adapter{
		Name: "pi", Label: "pi", Binary: "pi",
		NpmEntries: []string{"@earendil-works/pi-coding-agent/dist/bundle/cli.js", "@mariozechner/pi-coding-agent/dist/cli.js"},
		HelpArgs:   []string{"--help"},
		Caps: func(help []byte) []string {
			has := helpHas(help)
			return capList(map[string]bool{
				"headless": has("--mode", "json"), "tools": has("--tools"), "exclude-tools": has("--exclude-tools"),
				"no-session": has("--no-session"), "model": has("--model"), "no-mcp": has("--no-mcp"), "approve": has("--approve"),
			})
		},
		Levels:      []string{"read-only"},
		Enforcement: map[string]string{"read-only": "tools", "agent-config": "agent"},
		Flags: map[string]string{
			"read-only":    "--tools read,grep,find,ls",
			"agent-config": "(no flags — your ~/.pi/agent settings)",
		},
		Efforts:    []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"},
		EffortArgs: func(level string) []string { return []string{"--thinking", level} },
		ListModels: piListModels,
		Args:       piArgs,
		Classify:   piClassify,
		Final:      piFinal,
		Failure:    piFailure,
		Usage:      piUsage,
		Context: func(home, _ string) []string {
			ph := envHome("PI_CODING_AGENT_DIR", home, ".pi", "agent")
			return []string{filepath.Join(ph, "skills"), filepath.Join(ph, "prompts"), filepath.Join(ph, "AGENTS.md"),
				filepath.Join(home, ".agents", "skills")}
		},
		ToolNote: `pi has no sandbox: loopbee runs it only at read-only (read, grep, find, ls; no shell, no edits; MCP tools
  only when the Loop allows the agent's own MCP tools) or at agent-config, where the user's own pi configuration decides and pi can do anything the user can.
  For a pi Loop that changes code, choose agent-config only if the user asks for it, and keep a gate after it.`,
		MCPServers: func(home, project string) []string {
			names := mcpServerFileKeys(filepath.Join(envHome("PI_CODING_AGENT_DIR", home, ".pi", "agent"), "mcp.json"))
			if project != "" {
				names = append(names, mcpServerFileKeys(filepath.Join(project, ".pi", "mcp.json"))...)
			}
			return names
		},
		MCPFlags: map[string]string{
			"on":  "pi asks no approvals; in a worktree of a project you trust in pi, --approve so its .pi/mcp.json loads",
			"off": "--no-mcp, or --exclude-tools mcp__*,list_mcp_resources,list_mcp_resource_templates,read_mcp_resource",
		},
	})
}

// piTrusted reports whether pi's trust.json trusts the project (the nearest folder with a decision wins).
func piTrusted(home, project string) bool {
	m := readJSONC(filepath.Join(envHome("PI_CODING_AGENT_DIR", home, ".pi", "agent"), "trust.json"))
	for dir := filepath.Clean(project); ; dir = filepath.Dir(dir) {
		if v, ok := m[dir].(bool); ok {
			return v
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// piListModels parses `pi --list-models` table output.
func piListModels(ctx context.Context, base []string, env []string) ([]Model, error) {
	// Run in a temp directory to avoid side effects in the current directory.
	dir, err := os.MkdirTemp("", "loopbee-list-models-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out, err := runCommandEnv(ctx, dir, append(append([]string{}, base...), "--list-models"), nil, nil, env)
	if err != nil {
		return nil, err
	}
	var models []Model
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] == "provider" {
			continue
		}
		// Format: provider model context max-out thinking images
		// ID is provider/model
		id := fields[0] + "/" + fields[1]
		models = append(models, Model{ID: id})
	}
	return models, nil
}

func piArgs(inv agentInvocation, caps []string) ([]string, []string) {
	argv := []string{"--mode", "json"}
	if hasCap(caps, "no-session") {
		argv = append(argv, "--no-session")
	}
	if inv.Sandbox == "read-only" {
		// --tools keeps MCP tools unless an entry starts with mcp__; they are disabled unless the Loop allows them.
		argv = append(argv, "--tools", "read,grep,find,ls")
	}
	switch {
	case inv.MCP:
		// pi reads a project's .pi/mcp.json only once the project is trusted, and a worktree is another folder.
		if hasCap(caps, "approve") && inv.Project != "" && inv.Home != "" && filepath.Clean(inv.Dir) != filepath.Clean(inv.Project) && piTrusted(inv.Home, inv.Project) {
			argv = append(argv, "--approve")
		}
	case inv.Sandbox != "agent-config":
		if hasCap(caps, "no-mcp") {
			argv = append(argv, "--no-mcp")
		} else {
			argv = append(argv, "--exclude-tools", "mcp__*,list_mcp_resources,list_mcp_resource_templates,read_mcp_resource")
		}
	}
	if inv.Model != "" {
		argv = append(argv, "--model", inv.Model)
	}
	if inv.Effort != "" {
		if ad, ok := adapterFor(inv.Name); ok && ad.EffortArgs != nil {
			argv = append(argv, ad.EffortArgs(inv.Effort)...)
		}
	}
	return argv, nil
}

func piClassify(line string) (string, bool) {
	var ev map[string]any
	if !strings.HasPrefix(strings.TrimSpace(line), "{") {
		return "agent.raw", false
	}
	jsonLines([]byte(line), func(_ string, e map[string]any) { ev = e })
	t := str(ev, "type")
	switch t {
	case "session", "agent_start", "turn_start", "turn_end", "message_start", "message_update", "message_end",
		"tool_execution_start", "tool_execution_update", "tool_execution_end", "agent_settled", "compaction_start", "compaction_end",
		"queue_update", "auto_retry_start", "auto_retry_end":
		return "agent." + t, false
	case "agent_end":
		// The run ended and will not be retried: a final assistant message that stopped with an error is a failure.
		if ev["willRetry"] == true {
			return "agent.agent_end", false
		}
		msgs, _ := ev["messages"].([]any)
		for i := len(msgs) - 1; i >= 0; i-- {
			if m, ok := msgs[i].(map[string]any); ok && str(m, "role") == "assistant" {
				return "agent.agent_end", str(m, "stopReason") == "error"
			}
		}
		return "agent.agent_end", false
	}
	if t != "" {
		return "agent." + t, false
	}
	return "agent.raw", false
}

// piAssistant calls each completed assistant message (message_end) in order.
func piAssistant(output []byte, each func(m map[string]any)) {
	jsonLines(output, func(_ string, ev map[string]any) {
		if str(ev, "type") == "message_end" {
			if m := obj(ev, "message"); str(m, "role") == "assistant" {
				each(m)
			}
		}
	})
}

func piFinal(output []byte) (string, map[string]any) {
	last := ""
	piAssistant(output, func(m map[string]any) {
		var parts []string
		content, _ := m["content"].([]any)
		for _, c := range content {
			if b, ok := c.(map[string]any); ok && str(b, "type") == "text" && str(b, "text") != "" {
				parts = append(parts, str(b, "text"))
			}
		}
		if len(parts) > 0 {
			last = strings.Join(parts, "\n")
		}
	})
	return last, nil
}

func piFailure(output []byte) string {
	reason := ""
	piAssistant(output, func(m map[string]any) {
		if str(m, "stopReason") == "error" && str(m, "errorMessage") != "" {
			reason = str(m, "errorMessage")
		} else {
			reason = ""
		}
	})
	return reason
}

// piUsage sums the usage of every completed assistant message; cost is pi's own figure.
func piUsage(output []byte) Usage {
	var u Usage
	piAssistant(output, func(m map[string]any) {
		us := obj(m, "usage")
		u.Input += num(us, "input")
		u.CacheRead += num(us, "cacheRead")
		u.CacheWrite += num(us, "cacheWrite")
		u.Output += num(us, "output")
		u.CostUSD += fnum(obj(us, "cost"), "total")
		model := str(m, "model")
		if p := str(m, "provider"); p != "" && model != "" && !strings.Contains(model, "/") {
			model = p + "/" + model
		}
		u.Model = joinUnique(u.Model, model)
	})
	return u
}
