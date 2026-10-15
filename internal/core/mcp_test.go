package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mcpHome is a developer's machine where every agent has its own MCP servers: a browser, a database, a console.
func mcpHome(t *testing.T, project string) string {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(k, home)
	}
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(k, "")
	}
	write := func(rel, text string) {
		p := filepath.Join(home, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0700)
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(".codex/config.toml", "model = \"gpt-x\"\n\n[mcp_servers.chrome]\ncommand = \"npx\"\n\n[mcp_servers.\"db\".env]\nX = \"1\"\n\n[mcp_servers.chrome.tools.navigate]\napproval_mode = \"prompt\"\n\n[mcp_servers.\"my.server\"]\ncommand = \"x\"\n\n[mcp_servers]\ninline-srv = { command = \"x\" }\n\n[profiles.p]\nmodel = \"y\"\n")
	write(".codex/plugins/cache/openai-curated/browser-use/1.0.0/.codex-plugin/plugin.json", `{"name":"browser-use","mcpServers":"./.mcp.json"}`)
	write(".codex/plugins/cache/openai-curated/browser-use/1.0.0/.mcp.json", `{"mcpServers":{"browser":{"command":"node"}}}`)
	write(".claude.json", `{"mcpServers":{"pg":{"command":"pg-mcp"}},"projects":{`+jsonString(project)+`:{"mcpServers":{"k8s-console":{"command":"console-mcp","env":{"TOKEN":"t"}}}}}}`)
	write(".gemini/settings.json", `{"mcpServers":{"grafana":{"command":"g"}}}`)
	write(".gemini/extensions/jira/gemini-extension.json", `{"name":"jira","mcpServers":{"jira":{"command":"j"}}}`)
	write(".config/opencode/opencode.jsonc", "{\n  // my servers\n  \"mcp\": {\n    \"chrome-devtools\": {\"type\": \"local\", \"command\": [\"npx\", \"x\"]}, /* trailing */\n  },\n}\n")
	write(".pi/agent/mcp.json", `{"mcpServers":{"playwright":{"command":"npx"}}}`)
	write(".pi/agent/trust.json", `{`+jsonString(filepath.Clean(project))+`:true}`)
	os.WriteFile(filepath.Join(project, ".mcp.json"), []byte(`{"mcpServers":{"project-api":{"command":"api"}}}`), 0600)
	return home
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

// The agent's own MCP tools are what reach a browser, a database or a console. An unattended Run cannot answer an
// approval prompt, so a Loop that allows them approves every one in advance, in each CLI's own terms; a Loop that
// does not disables them below agent-config.
func TestAgentMCPToolsPerCLI(t *testing.T) {
	project := t.TempDir()
	home := mcpHome(t, project)
	worktree := t.TempDir()
	scratch := t.TempDir()
	for agent, want := range map[string][]string{
		"codex": {"browser", "chrome", "db", "my.server"}, "claude": {"k8s-console", "pg", "project-api"}, "gemini": {"grafana", "jira"},
		"opencode": {"chrome-devtools"}, "pi": {"playwright"},
	} {
		if got := agentMCPServers(agent, home, project); !slices.Equal(got, want) {
			t.Errorf("%s servers: %v", agent, got)
		}
	}
	args := func(agent, level string, mcp bool, dir string) (string, []string) {
		ad, _ := adapterFor(agent)
		argv, env := buildAgentInvocation([]string{agent}, ad.Caps([]byte(fakeHelp[agent]+" --no-mcp --approve")), agentInvocation{
			Name: agent, Dir: dir, Sandbox: level, MCP: mcp, Home: home, Project: project, Scratch: scratch})
		return strings.Join(argv, " "), env
	}
	type c struct {
		agent, level string
		mcp          bool
		want, not    []string
	}
	for _, tc := range []c{
		{"codex", "read-only", true, []string{`-c mcp_servers.chrome.default_tools_approval_mode="approve"`, `mcp_servers.db.default_tools_approval_mode="approve"`}, []string{"enabled=false"}},
		{"codex", "workspace-network", false, []string{"-c mcp_servers.chrome.enabled=false", "plugins.browser-use@openai-curated.mcp_servers.browser.enabled=false"}, []string{"approve"}},
		{"codex", "agent-config", false, nil, []string{"mcp_servers"}},
		{"codex", "agent-config", true, []string{`mcp_servers.chrome.default_tools_approval_mode="approve"`}, nil},
		{"claude", "agent-config", false, nil, []string{"--settings", "--mcp-config"}},
		{"pi", "read-only", false, []string{"--tools read,grep,find,ls", "--no-mcp"}, []string{"--approve"}},
		{"pi", "read-only", true, []string{"--tools read,grep,find,ls"}, []string{"--no-mcp", "--exclude-tools", "--approve"}},
		{"pi", "agent-config", false, nil, []string{"--no-mcp", "--exclude-tools"}},
	} {
		got, _ := args(tc.agent, tc.level, tc.mcp, project)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s %s mcp=%v: %q lacks %q", tc.agent, tc.level, tc.mcp, got, w)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(got, n) {
				t.Errorf("%s %s mcp=%v: %q has %q", tc.agent, tc.level, tc.mcp, got, n)
			}
		}
	}
	// `codex mcp list` decides which servers are on: a server the user switched off stays off (on the real Codex
	// 0.162.1 a policy entry for a disabled plugin server switched it on). Only servers of the user's config.toml
	// (tables or inline) and of plugins are named: a key for "nexscope", defined in another layer, stopped the real
	// Codex with “invalid transport in mcp_servers.nexscope”.
	ad, _ := adapterFor("codex")
	listed := func(mcp bool) string {
		argv, _ := buildAgentInvocation([]string{"codex"}, ad.Caps([]byte(fakeHelp["codex"])), agentInvocation{Name: "codex", Dir: project, Sandbox: "workspace-write",
			MCP: mcp, Home: home, Project: project, MCPListed: true,
			MCPList: []mcpServerState{{"chrome", true}, {"inline-srv", true}, {"nexscope", true}, {"browser", false}, {"db", false}}})
		return strings.Join(argv, " ")
	}
	if got := listed(true); strings.Contains(got, "nexscope") || !strings.Contains(got, `mcp_servers.chrome.default_tools_approval_mode`) ||
		!strings.Contains(got, `mcp_servers.inline-srv.default_tools_approval_mode`) || strings.Contains(got, "browser") || strings.Contains(got, "mcp_servers.db") {
		t.Fatalf("codex listed on: %s", got)
	}
	if got := listed(false); strings.Contains(got, "nexscope") || !strings.Contains(got, "mcp_servers.chrome.enabled=false") {
		t.Fatalf("codex listed off: %s", got)
	}
	// The plugin server, when enabled, is addressed through its plugin.
	if got := strings.Join(func() []string {
		argv, _ := buildAgentInvocation([]string{"codex"}, nil, agentInvocation{Name: "codex", Dir: project, Sandbox: "read-only", Home: home, MCPListed: true, MCPList: []mcpServerState{{"browser", true}}})
		return argv
	}(), " "); !strings.Contains(got, "-c plugins.browser-use@openai-curated.mcp_servers.browser.enabled=false") {
		t.Fatalf("codex listed plugin off: %s", got)
	}
	// Without the list, the switch approves only config.toml servers: a plugin entry could switch a server on.
	if got, _ := args("codex", "read-only", true, project); strings.Contains(got, "plugins.") {
		t.Fatalf("codex unlisted on touches plugins: %s", got)
	}
	// Codex splits -c keys at dots, so a server name with a dot is never put on the command line.
	if got, _ := args("codex", "read-only", true, project); strings.Contains(got, "my.server") {
		t.Fatal(got)
	}

	// Claude Code: "mcp__*" allowed (with the project's .mcp.json servers) or denied; in a worktree the project's
	// local servers, which ~/.claude.json keeps under the checkout's path, come along.
	settingsOf := func(cmd string) map[string]any {
		i := strings.Index(cmd, "--settings ")
		if i < 0 {
			return nil
		}
		rest := cmd[i+len("--settings "):]
		var m map[string]any
		json.NewDecoder(strings.NewReader(rest)).Decode(&m)
		return m
	}
	got, _ := args("claude", "read-only", true, worktree)
	s := settingsOf(got)
	if perms := obj(s, "permissions"); s["enableAllProjectMcpServers"] != true || !strings.Contains(toJSON(perms["allow"]), "mcp__*") || perms["deny"] != nil {
		t.Fatalf("claude on: %v", s)
	}
	if !strings.Contains(got, `--mcp-config {"mcpServers":{"k8s-console"`) {
		t.Fatalf("claude worktree lacks the local servers: %s", got)
	}
	if got, _ := args("claude", "read-only", true, project); strings.Contains(got, "--mcp-config") {
		t.Fatal("the checkout itself already has its local servers")
	}
	got, _ = args("claude", "workspace-write", false, project)
	if s := settingsOf(got); !strings.Contains(toJSON(obj(s, "permissions")["deny"]), "mcp__*") || obj(s, "sandbox") == nil {
		t.Fatalf("claude off: %v", s)
	}
	got, _ = args("claude", "agent-config", true, project)
	if s := settingsOf(got); obj(s, "sandbox") != nil || !strings.Contains(toJSON(obj(s, "permissions")["allow"]), "mcp__*") {
		t.Fatalf("claude agent-config adds only the MCP approval: %v", s)
	}

	// Gemini CLI: one policy rule for every MCP tool; at agent-config the user's own policies stay in force.
	policy := func() string { b, _ := os.ReadFile(filepath.Join(scratch, "gemini-policy.toml")); return string(b) }
	args("gemini", "read-only", false, project)
	if p := policy(); !strings.Contains(p, "mcpName = \"*\"\ndecision = \"deny\"") {
		t.Fatalf("gemini off: %s", p)
	}
	args("gemini", "workspace-network", true, project)
	if p := policy(); !strings.Contains(p, "mcpName = \"*\"\ndecision = \"allow\"") {
		t.Fatalf("gemini on: %s", p)
	}
	os.MkdirAll(filepath.Join(home, ".gemini", "policies"), 0700)
	got, _ = args("gemini", "agent-config", true, project)
	if !strings.Contains(got, "--policy "+filepath.Join(home, ".gemini", "policies")+" --policy "+filepath.Join(scratch, "gemini-policy.toml")) || strings.Contains(policy(), "activate_skill") {
		t.Fatalf("gemini agent-config: %s\n%s", got, policy())
	}
	if got, _ := args("gemini", "agent-config", false, project); strings.Contains(got, "--policy") {
		t.Fatal("gemini agent-config without MCP adds a policy")
	}

	// OpenCode: MCP tools are named <server>_<tool> and allowed by default, so read-only denies "*_*".
	_, env := args("opencode", "read-only", false, project)
	if len(env) != 1 || !strings.Contains(env[0], `"*_*":"deny"`) || !strings.Contains(env[0], `"codesearch":"deny"`) {
		t.Fatalf("opencode off: %v", env)
	}
	_, env = args("opencode", "read-only", true, project)
	if len(env) != 1 || strings.Contains(env[0], `"*_*"`) || !strings.Contains(env[0], `"chrome-devtools_*":"allow"`) || !strings.Contains(env[0], `"bash":"deny"`) {
		t.Fatalf("opencode on: %v", env)
	}
	if _, env := args("opencode", "agent-config", false, project); len(env) != 0 {
		t.Fatalf("opencode agent-config without MCP: %v", env)
	}

	// pi reads a project's .pi/mcp.json only in a trusted project; a worktree of a project the user trusts gets
	// --approve, an untrusted one does not.
	if got, _ := args("pi", "agent-config", true, worktree); !strings.Contains(got, "--approve") {
		t.Fatalf("pi trusted worktree: %s", got)
	}
	os.WriteFile(filepath.Join(home, ".pi", "agent", "trust.json"), []byte(`{`+jsonString(filepath.Clean(project))+`:false}`), 0600)
	if got, _ := args("pi", "agent-config", true, worktree); strings.Contains(got, "--approve") {
		t.Fatalf("pi untrusted worktree: %s", got)
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// Codex refused the browser MCP call the task needed and the agent only said “blocked”; the person could not tell
// that one switch was missing. The refusal reaches the missing list, and with the switch on the same Run succeeds.
func TestRefusedMCPToolReachesTheRunAndTheSwitchFixesIt(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			a := testApp(t)
			useFakeAgents(t, a, "mcp")
			t.Setenv("LOOPBEE_FAKE_MCP_LIST", `[{"name":"chrome","enabled":true},{"name":"off","enabled":false}]`)
			l := agentLoop(t, agent)
			mcpHome(t, l.Directory)
			a.DetectAgents()
			setMaxSandbox(t, a, "agent-config")
			l.Sandbox = "workspace-write"
			if agent == "codex" {
				l.Sandbox = "workspace-network"
			}
			run := func(l Loop) Run {
				t.Helper()
				l, err := a.SaveLoop(l)
				if err != nil {
					t.Fatal(err)
				}
				r, err := a.Start(l.ID)
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 3000; i++ {
					if r, _ = a.Run(r.ID); r.Finished != nil {
						return r
					}
					sleepMs(10)
				}
				t.Fatal("run did not finish")
				return r
			}
			r := run(l)
			if r.Status != "blocked" || r.Result == nil || len(r.Result.Missing) != 1 || !strings.Contains(r.Result.Missing[0], "chrome") || !strings.Contains(r.Result.Missing[0], "Use the agent's own MCP tools") {
				t.Fatalf("%s %+v", r.Status, r.Result)
			}
			l.AgentMCP = true
			if r := run(l); r.Status != "completed" || r.Result.Summary != "ConfigMap updated through chrome" {
				t.Fatalf("with the switch: %s %+v", r.Status, r.Result)
			}
		})
	}
}

func TestMCPHelpers(t *testing.T) {
	if got := sandboxBlocked([]byte("[pid] bootstrap_check_in org.chromium.Chromium.MachPortRendezvousServer.1: Permission denied (1100)")); len(got) != 1 || !strings.Contains(got[0], "agent-config") {
		t.Fatal(got)
	}
	gem := mcpRefused("gemini", []byte(`{"type":"tool_result","status":"error","error":{"message":"Tool execution for \"navigate (chrome MCP Server)\" requires user confirmation, which is not supported in non-interactive mode."}}`), false)
	if len(gem) != 1 || !strings.Contains(gem[0], "navigate (chrome MCP Server)") || !strings.Contains(gem[0], "turn on") {
		t.Fatal(gem)
	}
	// With the switch on, a refusal means the server is defined where loopbee cannot approve it.
	cx := mcpRefused("codex", []byte(`{"type":"item.completed","item":{"type":"mcp_tool_call","server":"nexscope","tool":"query","status":"failed","error":{"message":"MCP tool call requires approval, but approval policy is never"}}}`), true)
	if len(cx) != 1 || !strings.Contains(cx[0], "nexscope.query") || !strings.Contains(cx[0], "default_tools_approval_mode") {
		t.Fatal(cx)
	}
	if got := mcpRefused("claude", []byte("Claude requested permissions to use Bash, but you haven't granted it yet."), false); len(got) != 0 {
		t.Fatal("only MCP tools", got)
	}
	if got := string(stripJSONC([]byte("{\"u\": \"http://x//y\", /* c */ \"a\": [1,],}\n// end"))); !json.Valid([]byte(got)) || !strings.Contains(got, "http://x//y") {
		t.Fatal(got)
	}
	r := &AgentResult{Missing: []string{"x"}}
	mergeMissing(r, []string{"x", "y"})
	if !slices.Equal(r.Missing, []string{"x", "y"}) {
		t.Fatal(r.Missing)
	}
}

// The designer is told what agentMcp gives this user, and a proposal that needs the agent's tools keeps the switch.
func TestAuthorKnowsTheAgentsMCPServers(t *testing.T) {
	project := t.TempDir()
	home := mcpHome(t, project)
	if got := designerMCP("claude", home, project); !strings.Contains(got, "k8s-console, pg, project-api") || !strings.Contains(got, "agentMcp true") {
		t.Fatal(got)
	}
	if designerMCP("claude", t.TempDir(), "") != "" {
		t.Fatal("no servers, no sentence")
	}
	if p := authorPrompt(AuthorRequest{}, "/x", "en", "UTC", "codex", "workspace-network", Loop{}); !strings.Contains(p, "agentMcp: true when the work uses the agent's own MCP tools") {
		t.Fatal("prompt")
	}
	if l := normalizeDraft(Loop{AgentMCP: true, Steps: []Step{{Kind: "act", Name: "x", Prompt: "y"}}}, Loop{}, "codex"); !l.AgentMCP {
		t.Fatal("dropped")
	}
}
