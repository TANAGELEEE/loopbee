package core

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// OpenCode: `opencode run --format json`. OpenCode has no sandbox; it has per-tool permissions (allow, ask, deny),
// and a non-interactive run rejects every "ask". loopbee promises read-only by denying edits, shell, web and
// subagents through OPENCODE_CONFIG_CONTENT; for anything more the user's own configuration decides (agent-config).
func init() {
	registerAdapter(&adapter{
		Name: "opencode", Label: "OpenCode", Binary: "opencode",
		NpmEntries: []string{"opencode-ai/bin/opencode", "@opencode/cli/bin/opencode.cjs"},
		HelpArgs:   []string{"run", "--help"},
		Caps: func(help []byte) []string {
			has := helpHas(help)
			return capList(map[string]bool{"headless": has("--format", "json"), "model": has("--model"), "auto": has("--auto")})
		},
		Levels:      []string{"read-only"},
		Enforcement: map[string]string{"read-only": "tools", "agent-config": "agent"},
		Flags: map[string]string{
			"read-only":    "OPENCODE_CONFIG_CONTENT: edit, bash, webfetch, websearch, task denied",
			"agent-config": "(no flags — your opencode.json permissions)",
		},
		Args:     opencodeArgs,
		Classify: opencodeClassify,
		Final:    opencodeFinal,
		Failure:  opencodeFailure,
		Usage:    opencodeUsage,
		Context: func(home, _ string) []string {
			oc := filepath.Join(envHome("XDG_CONFIG_HOME", home, ".config"), "opencode")
			return []string{filepath.Join(oc, "skills"), filepath.Join(oc, "agents"), filepath.Join(oc, "commands"),
				filepath.Join(oc, "AGENTS.md"), filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".agents", "skills")}
		},
		ToolNote: `OpenCode has no sandbox: loopbee runs it only at read-only (edits, shell, web and subagents denied
  through its permission settings) or at agent-config, where the user's own opencode.json decides. For an OpenCode
  Loop that changes code, choose agent-config only if the user asks for it, and keep a gate after it.`,
		MCPServers: func(home, project string) []string {
			var names []string
			for _, f := range opencodeConfigFiles(home, project) {
				names = append(names, jsonObjectKeys(f, "mcp")...)
			}
			return names
		},
		MCPFlags: map[string]string{
			"on":  `OPENCODE_CONFIG_CONTENT: "<server>_*": "allow" for each server in opencode.json`,
			"off": `OPENCODE_CONFIG_CONTENT: "*_*": "deny" (MCP tool names are <server>_<tool>)`,
		},
	})
}

// opencodeConfigFiles are the global and project config files OpenCode reads.
func opencodeConfigFiles(home, project string) []string {
	oc := filepath.Join(envHome("XDG_CONFIG_HOME", home, ".config"), "opencode")
	files := []string{filepath.Join(oc, "opencode.json"), filepath.Join(oc, "opencode.jsonc"), filepath.Join(oc, "config.json")}
	if project != "" {
		files = append(files, filepath.Join(project, "opencode.json"), filepath.Join(project, "opencode.jsonc"),
			filepath.Join(project, ".opencode", "opencode.json"), filepath.Join(project, ".opencode", "opencode.jsonc"))
	}
	return files
}

// opencodeToolPrefix is how OpenCode names a server's tools: <server>_<tool>, other characters replaced by "_".
func opencodeToolPrefix(server string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, server) + "_"
}

func opencodeArgs(inv agentInvocation, caps []string) ([]string, []string) {
	argv := []string{"run", "--format", "json"}
	if inv.Model != "" {
		argv = append(argv, "--model", inv.Model)
	}
	permission := map[string]any{}
	if inv.MCP && inv.Home != "" {
		// Approved in advance: a non-interactive run rejects every "ask".
		for _, server := range agentMCPServers("opencode", inv.Home, inv.Dir) {
			permission[opencodeToolPrefix(server)+"*"] = "allow"
		}
	}
	if inv.Sandbox == "read-only" {
		external := map[string]string{"*": "deny"}
		for _, p := range inv.ReadPaths {
			external[filepath.ToSlash(p)+"/**"] = "allow"
			external[filepath.ToSlash(p)] = "allow"
		}
		for k, v := range map[string]any{"edit": "deny", "bash": "deny", "webfetch": "deny", "websearch": "deny", "codesearch": "deny", "task": "deny",
			"external_directory": external} {
			permission[k] = v
		}
		if !inv.MCP {
			// Every MCP tool is named <server>_<tool>; rules are matched in order and "*_*" sorts first, so the
			// read-only rules after it still apply to the built-in tools.
			permission["*_*"] = "deny"
		}
	}
	if len(permission) == 0 {
		return argv, nil
	}
	b, _ := json.Marshal(map[string]any{"permission": permission})
	return argv, []string{"OPENCODE_CONFIG_CONTENT=" + string(b)}
}

func opencodeClassify(line string) (string, bool) {
	var ev map[string]any
	if !strings.HasPrefix(strings.TrimSpace(line), "{") {
		return "agent.raw", false
	}
	jsonLines([]byte(line), func(_ string, e map[string]any) { ev = e })
	switch t := str(ev, "type"); t {
	case "text", "reasoning", "tool_use", "step_start", "step_finish":
		return "agent." + t, false
	case "error":
		return "agent.error", true
	}
	return "agent.raw", false
}

// opencodeFinal is the text after the last tool call (each text event carries a whole text block).
func opencodeFinal(output []byte) (string, map[string]any) {
	var parts []string
	jsonLines(output, func(_ string, ev map[string]any) {
		switch str(ev, "type") {
		case "text":
			if t := str(obj(ev, "part"), "text"); t != "" {
				parts = append(parts, t)
			}
		case "tool_use":
			parts = nil
		}
	})
	return strings.Join(parts, "\n"), nil
}

func opencodeFailure(output []byte) string {
	reason := ""
	jsonLines(output, func(_ string, ev map[string]any) {
		if str(ev, "type") != "error" {
			return
		}
		e := obj(ev, "error")
		switch {
		case str(e, "message") != "":
			reason = str(e, "message")
		case str(obj(e, "data"), "message") != "":
			reason = str(obj(e, "data"), "message")
		case str(e, "name") != "":
			reason = str(e, "name")
		}
	})
	return reason
}

// opencodeUsage sums step_finish tokens and cost; OpenCode reports reasoning apart from output.
func opencodeUsage(output []byte) Usage {
	var u Usage
	jsonLines(output, func(_ string, ev map[string]any) {
		if str(ev, "type") != "step_finish" {
			return
		}
		part := obj(ev, "part")
		tk := obj(part, "tokens")
		reasoning := num(tk, "reasoning")
		u.Input += num(tk, "input")
		u.Output += num(tk, "output") + reasoning
		u.Reasoning += reasoning
		u.CacheRead += num(obj(tk, "cache"), "read")
		u.CacheWrite += num(obj(tk, "cache"), "write")
		u.CostUSD += fnum(part, "cost")
	})
	return u
}
