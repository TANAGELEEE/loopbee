package core

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Codex CLI: `codex exec --json`, its own OS sandbox for every level (see docs/subsystems/agents.md).
func init() {
	registerAdapter(&adapter{
		Name: "codex", Label: "Codex CLI", Binary: "codex",
		NpmEntries: []string{"@openai/codex/bin/codex.js"},
		HelpArgs:   []string{"exec", "--help"},
		Caps: func(help []byte) []string {
			has := helpHas(help)
			return capList(map[string]bool{
				"headless": has("--json", "--sandbox"), "last-message": has("--output-last-message"), "schema": has("--output-schema"),
				"add-dir": has("--add-dir"), "skip-git-check": has("--skip-git-repo-check"), "model": has("--model"),
			})
		},
		Levels:      []string{"read-only", "workspace-write", "workspace-network"},
		Enforcement: map[string]string{"read-only": "sandbox", "workspace-write": "sandbox", "workspace-network": "sandbox", "agent-config": "agent"},
		Flags: map[string]string{
			"read-only": "--sandbox read-only", "workspace-write": "--sandbox workspace-write",
			"workspace-network": "--sandbox workspace-write -c sandbox_workspace_write.network_access=true",
			"agent-config":      "(no flags — your config.toml / profile)",
		},
		// Efforts: codex accepts model_reasoning_effort but doesn't document valid values in --help or reject
		// invalid ones; cannot confirm the list without a model turn.
		EffortArgs: func(level string) []string { return []string{"-c", "model_reasoning_effort=\"" + level + "\""} },
		Args:       codexArgs,
		Classify:   codexClassify,
		Final:      codexFinal,
		Failure:    codexFailure,
		Usage:      codexUsage,
		DefaultModel: func(home string) string {
			return tomlTopLevel(filepath.Join(envHome("CODEX_HOME", home, ".codex"), "config.toml"), "model")
		},
		Context: func(home, _ string) []string {
			ch := envHome("CODEX_HOME", home, ".codex")
			return []string{filepath.Join(ch, "skills"), filepath.Join(ch, "prompts"), filepath.Join(ch, "memories"),
				filepath.Join(ch, "AGENTS.md"), filepath.Join(home, ".agents", "skills")}
		},
		ToolNote: `At read-only, Codex can run shell commands inside a read-only sandbox (no writes, no network).`,
		MCPServers: func(home, project string) []string {
			ch := envHome("CODEX_HOME", home, ".codex")
			names := tomlTableNames(filepath.Join(ch, "config.toml"), "mcp_servers")
			if project != "" {
				names = append(names, tomlTableNames(filepath.Join(project, ".codex", "config.toml"), "mcp_servers")...)
			}
			for _, p := range codexPluginServers(ch) {
				names = append(names, p[1])
			}
			return names
		},
		MCPFlags: map[string]string{
			"on":  `-c mcp_servers.<server>.default_tools_approval_mode="approve" for each server in config.toml, and plugins.<plugin>.mcp_servers.<server>… for plugin servers`,
			"off": `-c mcp_servers.<server>.enabled=false (and the same for plugin servers)`,
		},
		Refused: codexRefused,
		ListMCP: func(ctx context.Context, base []string, dir string, env []string) ([]mcpServerState, error) {
			out, err := runCommandEnv(ctx, dir, append(append([]string{}, base...), "mcp", "list", "--json"), nil, nil, env)
			if err != nil {
				return nil, err
			}
			var list []mcpServerState
			return list, decodeJSONArray(out, &list)
		},
	})
}

// codexMCPArgs approves (Loop.AgentMCP) or disables the MCP servers Codex will start. Codex's own
// "approval_policy" cannot do it: exec always runs with "never", which refuses every MCP call needing approval.
// Only servers defined in the user's config.toml (mcp_servers.<server>) or by an installed plugin
// (plugins.<plugin>.mcp_servers.<server>) can be named: on the real Codex 0.162.1 a key for a server defined in
// another layer (a project's .codex/config.toml) was an incomplete server entry, and Codex refused to start. When
// Codex lists its servers (`codex mcp list --json`), only enabled ones are named, because a plugin entry would
// switch a server the user turned off back on; without the list, plugin servers are only ever disabled.
func codexMCPArgs(inv agentInvocation) []string {
	if inv.Home == "" || (!inv.MCP && inv.Sandbox == "agent-config") {
		return nil
	}
	setting := `enabled=false`
	if inv.MCP {
		setting = `default_tools_approval_mode="approve"`
	}
	safe := func(s string) bool { return s != "" && !strings.ContainsAny(s, ".= \t\"'[]") }
	ch := envHome("CODEX_HOME", inv.Home, ".codex")
	user := codexUserServers(ch)
	plugin := map[string]string{} // server -> plugin id
	for _, p := range codexPluginServers(ch) {
		plugin[p[1]] = p[0]
	}
	var argv []string
	add := func(server string) {
		if id, ok := plugin[server]; ok && !slices.Contains(user, server) {
			if safe(id) && safe(server) {
				argv = append(argv, "-c", "plugins."+id+".mcp_servers."+server+"."+setting)
			}
		} else if slices.Contains(user, server) && safe(server) {
			argv = append(argv, "-c", "mcp_servers."+server+"."+setting)
		}
	}
	if inv.MCPListed {
		for _, s := range inv.MCPList {
			if s.Enabled {
				add(s.Name)
			}
		}
		return argv
	}
	for _, name := range user {
		add(name)
	}
	if !inv.MCP {
		for server := range plugin {
			add(server)
		}
	}
	return argv
}

// codexUserServers are the servers defined in the user's config.toml, as tables ([mcp_servers.x]) or inline
// ([mcp_servers] with x = { … }).
func codexUserServers(codexHome string) []string {
	path := filepath.Join(codexHome, "config.toml")
	names := tomlTableNames(path, "mcp_servers")
	f, err := os.Open(path)
	if err != nil {
		return names
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	in := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = line == "[mcp_servers]"
			continue
		}
		if k, v, ok := strings.Cut(line, "="); in && ok && strings.HasPrefix(strings.TrimSpace(v), "{") {
			if k = strings.Trim(strings.TrimSpace(k), `"`); k != "" && !slices.Contains(names, k) {
				names = append(names, k)
			}
		}
	}
	return names
}

// codexPluginServers lists [plugin id, server] for the MCP servers of installed plugins
// (CODEX_HOME/plugins/cache/<marketplace>/<plugin>/<version>/, server files as Codex reads them).
func codexPluginServers(codexHome string) [][2]string {
	var out [][2]string
	add := func(id, server string) {
		p := [2]string{id, server}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	markets, _ := os.ReadDir(filepath.Join(codexHome, "plugins", "cache"))
	for _, m := range markets {
		plugins, _ := os.ReadDir(filepath.Join(codexHome, "plugins", "cache", m.Name()))
		for _, p := range plugins {
			versions, _ := os.ReadDir(filepath.Join(codexHome, "plugins", "cache", m.Name(), p.Name()))
			for _, v := range versions {
				root := filepath.Join(codexHome, "plugins", "cache", m.Name(), p.Name(), v.Name())
				if !v.IsDir() {
					continue
				}
				for _, s := range codexPluginServerNames(root) {
					add(p.Name()+"@"+m.Name(), s)
				}
			}
		}
	}
	return out
}

func codexPluginServerNames(root string) []string {
	for _, manifest := range []string{filepath.Join(root, ".codex-plugin", "plugin.json"), filepath.Join(root, "plugin.json")} {
		m := readJSONC(manifest)
		if m == nil {
			continue
		}
		switch v := m["mcpServers"].(type) {
		case string:
			return mcpServerFileKeys(filepath.Join(root, filepath.FromSlash(v)))
		case map[string]any:
			if inner, ok := v["mcpServers"].(map[string]any); ok {
				v = inner
			}
			keys := []string{}
			for k := range v {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			return keys
		}
		break
	}
	return mcpServerFileKeys(filepath.Join(root, ".mcp.json"))
}

// codexRefused names MCP calls Codex refused for want of an approval.
func codexRefused(output []byte) []string {
	var out []string
	jsonLines(output, func(_ string, ev map[string]any) {
		item := obj(ev, "item")
		if str(ev, "type") != "item.completed" || str(item, "type") != "mcp_tool_call" {
			return
		}
		msg := str(obj(item, "error"), "message")
		if res := obj(item, "result"); msg == "" && res != nil {
			b, _ := json.Marshal(res)
			msg = string(b)
		}
		if strings.Contains(msg, "approval") || strings.Contains(msg, "cancelled") {
			if n := str(item, "server") + "." + str(item, "tool"); !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	})
	if len(out) == 0 && strings.Contains(string(output), "approval policy is never") {
		out = append(out, "(unnamed)")
	}
	return out
}

func capList(m map[string]bool) []string {
	out := []string{}
	for _, k := range []string{"headless", "last-message", "schema", "add-dir", "skip-git-check", "tools", "settings", "permission-mode",
		"model", "sandbox", "approval-mode", "policy", "include-dirs", "skip-trust", "exclude-tools", "no-session", "auto", "no-mcp", "approve"} {
		if m[k] {
			out = append(out, k)
		}
	}
	return out
}

func codexArgs(inv agentInvocation, caps []string) ([]string, []string) {
	argv := []string{"exec", "--json", "--color", "never"}
	switch inv.Sandbox {
	case "read-only", "workspace-write":
		argv = append(argv, "--sandbox", inv.Sandbox)
	case "workspace-network":
		argv = append(argv, "--sandbox", "workspace-write", "-c", "sandbox_workspace_write.network_access=true")
	}
	if inv.Model != "" {
		argv = append(argv, "--model", inv.Model)
	}
	if inv.Effort != "" {
		if ad, ok := adapterFor(inv.Name); ok && ad.EffortArgs != nil {
			argv = append(argv, ad.EffortArgs(inv.Effort)...)
		}
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
	argv = append(argv, codexMCPArgs(inv)...)
	return append(argv, "-C", inv.Dir, "-"), nil
}

func codexClassify(line string) (string, bool) {
	var ev struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(line), &ev) == nil {
		switch ev.Type {
		case "thread.started", "turn.started", "turn.completed", "turn.failed", "item.started", "item.updated", "item.completed", "error":
			return "agent." + ev.Type, ev.Type == "turn.failed"
		}
	}
	return "agent.raw", false
}

func codexFinal(output []byte) (string, map[string]any) {
	last := ""
	jsonLines(output, func(_ string, ev map[string]any) {
		if item := obj(ev, "item"); str(ev, "type") == "item.completed" && str(item, "type") == "agent_message" {
			last = str(item, "text")
		}
	})
	return last, nil
}

func codexFailure(output []byte) string {
	reason := ""
	jsonLines(output, func(_ string, ev map[string]any) {
		switch str(ev, "type") {
		case "turn.failed":
			if m := str(obj(ev, "error"), "message"); m != "" {
				reason = m
			}
		case "error":
			if m := str(ev, "message"); m != "" {
				reason = m
			}
		}
	})
	return reason
}

// codexUsage sums turn.completed usage; input_tokens includes cached_input_tokens.
func codexUsage(output []byte) Usage {
	var u Usage
	jsonLines(output, func(_ string, ev map[string]any) {
		if str(ev, "type") != "turn.completed" {
			return
		}
		us := obj(ev, "usage")
		cached := num(us, "cached_input_tokens")
		u.Input += max(num(us, "input_tokens")-cached, 0)
		u.CacheRead += cached
		u.Output += num(us, "output_tokens")
		u.Reasoning += num(us, "reasoning_output_tokens")
	})
	return u
}

// tomlTopLevel reads a top-level string key (before any [table]) from a small TOML file.
func tomlTopLevel(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			return ""
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		return strings.Trim(v, `"'`)
	}
	return ""
}
