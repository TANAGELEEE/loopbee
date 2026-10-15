package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Claude Code: `claude -p --output-format stream-json`; tool limits at read-only, its OS sandbox for Bash at the
// write levels (macOS, Linux, WSL2 only; failIfUnavailable makes it refuse to run unsandboxed).
func init() {
	registerAdapter(&adapter{
		Name: "claude", Label: "Claude Code", Binary: "claude",
		NpmEntries: []string{"@anthropic-ai/claude-code/cli.js"},
		HelpArgs:   []string{"--help"},
		Caps: func(help []byte) []string {
			has := helpHas(help)
			return capList(map[string]bool{
				"headless": has("--print", "--output-format", "stream-json"), "schema": has("--json-schema"), "add-dir": has("--add-dir"),
				"tools": has("--tools"), "settings": has("--settings"), "permission-mode": has("--permission-mode"), "model": has("--model"),
			})
		},
		Levels:      []string{"read-only", "workspace-write", "workspace-network"},
		Enforcement: map[string]string{"read-only": "tools", "workspace-write": "sandbox", "workspace-network": "sandbox", "agent-config": "agent"},
		Flags: map[string]string{
			"read-only":         "--permission-mode dontAsk --tools Read,Grep,Glob,Skill",
			"workspace-write":   "--permission-mode acceptEdits + sandbox (no network)",
			"workspace-network": "--permission-mode acceptEdits + sandbox (allowedDomains *)",
			"agent-config":      "(no flags — your settings.json permissions)",
		},
		Args:     claudeArgs,
		Classify: claudeClassify,
		Final:    claudeFinal,
		Failure:  claudeFailure,
		Usage:    claudeUsage,
		Context: func(home, project string) []string {
			ch := claudeHome(home)
			return []string{filepath.Join(ch, "skills"), filepath.Join(ch, "plugins"), filepath.Join(ch, "agents"),
				filepath.Join(ch, "commands"), filepath.Join(ch, "CLAUDE.md"), claudeMemory(home, project),
				filepath.Join(home, ".agents", "skills")}
		},
		Memory: claudeMemory,
		ToolNote: `At read-only, Claude Code can read and search files and load skills, but it cannot run any shell
  command, so a skill's scripts do not run. If a read-only Loop needs command output (tests, logs, git status, a
  query), add a run step before the agent step, or choose workspace-write, where commands run inside Claude
  Code's sandbox. On native Windows Claude Code has no sandbox yet: at workspace-write or workspace-network it
  cannot start there, so prefer Codex for those levels on Windows.`,
		Explain: claudeExplain,
		MCPServers: func(home, project string) []string {
			cfg := claudeGlobalConfig(home)
			names := jsonObjectKeys(cfg, "mcpServers")
			if project != "" {
				names = append(names, claudeLocalServerNames(cfg, project)...)
				names = append(names, mcpServerFileKeys(filepath.Join(project, ".mcp.json"))...)
			}
			return names
		},
		MCPFlags: map[string]string{
			"on":  `--settings permissions.allow "mcp__*", enableAllProjectMcpServers; in a worktree also --mcp-config with the project's local servers`,
			"off": `--settings permissions.deny "mcp__*"`,
		},
		Refused: func(output []byte) []string {
			return refusedBetween(output, "Claude requested permissions to use ", ", but you haven't granted it yet", "%s", "mcp__")
		},
	})
}

// claudeGlobalConfig is Claude Code's ~/.claude.json (in CLAUDE_CONFIG_DIR when that is set).
func claudeGlobalConfig(home string) string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// claudeLocalServers are the servers added with `claude mcp add` (local scope) for the project: ~/.claude.json keeps
// them under the project's path, so a worktree, at another path, would not get them.
func claudeLocalServersSafe(cfg, project string) map[string]any {
	return obj(obj(obj(readJSONC(cfg), "projects"), filepath.Clean(project)), "mcpServers")
}

func claudeLocalServerNames(cfg, project string) []string {
	m := claudeLocalServersSafe(cfg, project)
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func claudeHome(home string) string { return envHome("CLAUDE_CONFIG_DIR", home, ".claude") }

// claudeMemory is ~/.claude/projects/<key>/memory for the project in dir.
func claudeMemory(home, dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(claudeHome(home), "projects", claudeProjectKey(dir), "memory")
}

func claudeArgs(inv agentInvocation, caps []string) ([]string, []string) {
	argv := []string{"-p", "--output-format", "stream-json", "--verbose"}
	switch inv.Sandbox {
	case "read-only":
		// Skill loads the user's skills (instructions only: without a shell their scripts cannot run).
		argv = append(argv, "--permission-mode", "dontAsk", "--tools", "Read,Grep,Glob,Skill")
	case "workspace-write", "workspace-network":
		argv = append(argv, "--permission-mode", "acceptEdits")
	}
	if inv.Sandbox != "agent-config" || inv.MCP {
		if settings := claudeSettings(inv.Sandbox, inv.ReadPaths, inv.MCP); settings != "" {
			argv = append(argv, "--settings", settings)
		}
	}
	if inv.MCP && inv.Project != "" && inv.Home != "" && filepath.Clean(inv.Dir) != filepath.Clean(inv.Project) {
		if local := claudeLocalServersSafe(claudeGlobalConfig(inv.Home), inv.Project); len(local) > 0 {
			b, _ := json.Marshal(map[string]any{"mcpServers": local})
			argv = append(argv, "--mcp-config", string(b))
		}
	}
	if inv.Model != "" {
		argv = append(argv, "--model", inv.Model)
	}
	if hasCap(caps, "add-dir") {
		for _, d := range inv.AddDirs {
			argv = append(argv, "--add-dir", d)
		}
	}
	if inv.Schema != "" && hasCap(caps, "schema") {
		argv = append(argv, "--json-schema", inv.Schema)
	}
	return argv, nil
}

// claudeSettings maps a Loopbee permission level to Claude Code settings: the OS sandbox for Bash (write levels)
// and read rules for ReadPaths. "" means no settings are needed.
func claudeSettings(sandbox string, readPaths []string, mcp bool) string {
	settings := map[string]any{}
	allow := []string{}
	deny := []string{}
	switch {
	case mcp:
		allow = append(allow, "mcp__*")
		settings["enableAllProjectMcpServers"] = true
	case sandbox != "agent-config":
		deny = append(deny, "mcp__*")
	}
	if sandbox == "agent-config" {
		// Only what the Loop allows; the user's own settings decide the rest.
		readPaths = nil
	}
	if sandbox == "workspace-write" || sandbox == "workspace-network" {
		domains := []string{}
		if sandbox == "workspace-network" {
			domains = []string{"*"}
		}
		settings["sandbox"] = map[string]any{
			"enabled": true, "autoAllowBashIfSandboxed": true, "allowUnsandboxedCommands": false, "failIfUnavailable": true,
			"network": map[string]any{"allowedDomains": domains},
		}
	}
	for _, p := range readPaths {
		info, err := os.Stat(p)
		allow = append(allow, claudeReadRule(p, err == nil && info.IsDir()))
	}
	if len(allow) > 0 || len(deny) > 0 {
		perms := map[string]any{}
		if len(allow) > 0 {
			perms["allow"] = allow
		}
		if len(deny) > 0 {
			perms["deny"] = deny
		}
		settings["permissions"] = perms
	}
	if len(settings) == 0 {
		return ""
	}
	b, _ := json.Marshal(settings)
	return string(b)
}

func claudeClassify(line string) (string, bool) {
	var ev struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		IsError bool   `json:"is_error"`
	}
	if json.Unmarshal([]byte(line), &ev) == nil {
		switch ev.Type {
		case "system", "assistant", "user", "stream_event":
			return "agent." + ev.Type, false
		case "result":
			return "agent.result", ev.IsError || (ev.Subtype != "" && ev.Subtype != "success")
		}
	}
	return "agent.raw", false
}

func claudeFinal(output []byte) (string, map[string]any) {
	last := ""
	var structured map[string]any
	jsonLines(output, func(_ string, ev map[string]any) {
		switch str(ev, "type") {
		case "assistant":
			var parts []string
			content, _ := obj(ev, "message")["content"].([]any)
			for _, c := range content {
				if m, ok := c.(map[string]any); ok && str(m, "type") == "text" && str(m, "text") != "" {
					parts = append(parts, str(m, "text"))
				}
			}
			if len(parts) > 0 {
				last = strings.Join(parts, "\n")
			}
		case "result":
			if r := str(ev, "result"); r != "" {
				last = r
			}
			if s := obj(ev, "structured_output"); s != nil {
				structured = s
			}
		}
	})
	return last, structured
}

func claudeFailure(output []byte) string {
	reason := ""
	jsonLines(output, func(_ string, ev map[string]any) {
		if errs, ok := ev["errors"].([]any); ok && len(errs) > 0 {
			if s, ok := errs[0].(string); ok {
				reason = s
				return
			}
		}
		if str(ev, "type") == "result" && ev["is_error"] == true && str(ev, "result") != "" {
			reason = str(ev, "result") // e.g. "You've hit your session limit · resets 10:20pm"
		}
	})
	return reason
}

// claudeUsage reads the result event (usage, total_cost_usd, modelUsage) and the model from system init.
func claudeUsage(output []byte) Usage {
	var u Usage
	jsonLines(output, func(_ string, ev map[string]any) {
		switch str(ev, "type") {
		case "system":
			if m := str(ev, "model"); m != "" && u.Model == "" {
				u.Model = m
			}
		case "result":
			us := obj(ev, "usage")
			u.Input += num(us, "input_tokens")
			u.CacheRead += num(us, "cache_read_input_tokens")
			u.CacheWrite += num(us, "cache_creation_input_tokens")
			u.Output += num(us, "output_tokens")
			u.CostUSD += fnum(ev, "total_cost_usd")
			if models := obj(ev, "modelUsage"); len(models) > 0 {
				names := []string{}
				for name := range models {
					names = append(names, name)
				}
				slices.Sort(names)
				u.Model = strings.Join(names, ", ")
			}
		}
	})
	return u
}

// claudeExplain: Claude Code suggests changing sandbox.failIfUnavailable, which Loopbee's flags override.
func claudeExplain(reason, goos string) string {
	if !strings.Contains(reason, "Sandbox required but unavailable") {
		return reason
	}
	if goos == "windows" {
		return reason + " — Claude Code cannot sandbox commands on native Windows yet, so it cannot run at workspace-write or workspace-network here. Switch this Loop to Codex, run Loopbee in WSL2, or choose the agent-config permission (Claude Code's own settings decide; raise the device limit in Settings › General first). Changing failIfUnavailable in Claude Code has no effect: Loopbee sets it."
	}
	return reason + " — Claude Code's sandbox needs bubblewrap and socat on Linux (install them, e.g. apt install bubblewrap socat), or switch this Loop to Codex, or choose the agent-config permission. Changing failIfUnavailable in Claude Code has no effect: Loopbee sets it."
}
