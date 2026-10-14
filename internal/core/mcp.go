package core

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"time"
)

// MCP tools are how agents reach a browser, a database, a console or a ticket system. An unattended Run has nobody
// to answer the CLI's approval prompt, so without help every such call is refused ("MCP tool call requires approval,
// but approval policy is never"). Loop.AgentMCP approves all of them in advance, per CLI; without it Loopbee
// disables them below agent-config, so a level such as read-only stays what it says. Loopbee adds no MCP servers of
// its own: the agent uses what it is set up with.

// agentMCPServers lists the MCP servers of the agent's own setup (sorted, unique). It may be incomplete.
func agentMCPServers(agent, home, project string) []string {
	a, ok := adapterFor(agent)
	if !ok || a.MCPServers == nil || home == "" {
		return nil
	}
	var out []string
	for _, n := range a.MCPServers(home, project) {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// mcpRefused lists MCP tool calls the CLI refused because nobody could approve them, for the Run's missing list.
// allowed: the Loop already approves the agent's MCP tools, so the server must be defined where Loopbee cannot
// approve it (Codex: outside the user's config.toml).
func mcpRefused(agent string, output []byte, allowed bool) []string {
	a, ok := adapterFor(agent)
	if !ok || a.Refused == nil {
		return nil
	}
	out := []string{}
	for _, name := range a.Refused(output) {
		note := mcpNote
		if allowed {
			note = mcpNoteAllowed
		}
		out = append(out, fmtLabel(note, name))
	}
	return out
}

// sandboxBlocked lists what the agent's sandbox stopped that people would not guess from the agent's report.
func sandboxBlocked(output []byte) []string {
	var out []string
	// Chromium registers a Mach service at start; the macOS sandboxes of Codex and Claude Code forbid that.
	if strings.Contains(string(output), "bootstrap_check_in") && strings.Contains(string(output), "Permission denied") {
		out = append(out, "a browser started from the shell (the sandbox on macOS does not let Chromium start; use the agent's browser MCP tool with “Use the agent's own MCP tools”, or the agent-config level)")
	}
	return out
}

// mergeMissing adds items to a result's missing list (deduplicated, at most 10).
func mergeMissing(r *AgentResult, items []string) {
	for _, m := range items {
		if m = firstLine(m, 200); m != "" && len(r.Missing) < 10 && !slices.Contains(r.Missing, m) {
			r.Missing = append(r.Missing, m)
		}
	}
}

// tomlTableNames returns N for every table header [prefix.N], [prefix."N"] or [prefix.N.sub] in a TOML file.
func tomlTableNames(path, prefix string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "[") || strings.HasPrefix(line, "[[") {
			continue
		}
		end := strings.Index(line, "]")
		if end < 0 {
			continue
		}
		key := strings.TrimSpace(line[1:end])
		rest, ok := strings.CutPrefix(key, prefix+".")
		if !ok {
			continue
		}
		name := rest
		if strings.HasPrefix(rest, `"`) {
			if i := strings.Index(rest[1:], `"`); i >= 0 {
				name = rest[1 : i+1]
			}
		} else if i := strings.Index(rest, "."); i >= 0 {
			name = rest[:i]
		}
		if name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// readJSONC decodes a JSON file that may contain comments and trailing commas (OpenCode, VS Code style).
func readJSONC(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(stripJSONC(b), &m) != nil {
		return nil
	}
	return m
}

// jsonObjectKeys returns the keys of the object at fields in a JSON(C) file ("" fields = the top level).
func jsonObjectKeys(path string, fields ...string) []string {
	m := readJSONC(path)
	for _, f := range fields {
		m = obj(m, f)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// stripJSONC removes // and /* */ comments outside strings and commas before } or ].
func stripJSONC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inString, escaped := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
		case c == ']' || c == '}':
			j := len(out) - 1
			for j >= 0 && strings.ContainsRune(" \t\r\n", rune(out[j])) {
				j--
			}
			if j >= 0 && out[j] == ',' {
				out = append(out[:j], out[j+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// mcpServerFileKeys reads server names from an MCP file: {"mcpServers": {...}} or a bare map of servers.
func mcpServerFileKeys(path string) []string {
	m := readJSONC(path)
	if servers, ok := m["mcpServers"].(map[string]any); ok {
		m = servers
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// refusedBetween collects the text between start and end after each occurrence of start (deduplicated), keeping
// only names with the given prefix ("" = all).
func refusedBetween(output []byte, start, end, label, prefix string) []string {
	var out []string
	s := strings.ReplaceAll(string(output), `\"`, `"`) // the text may sit inside a JSON string
	for {
		i := strings.Index(s, start)
		if i < 0 {
			return out
		}
		s = s[i+len(start):]
		j := strings.Index(s, end)
		if j < 0 || j > 200 || !strings.HasPrefix(s, prefix) {
			continue
		}
		item := fmtLabel(label, s[:j])
		if !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
}

func fmtLabel(label, name string) string {
	return strings.ReplaceAll(label, "%s", strings.Trim(name, `"\`))
}

// mcpNote is the missing-list text for an MCP tool nobody could approve.
const (
	mcpNote        = "MCP tool %s: needs an approval nobody can give in an unattended Run (turn on “Use the agent's own MCP tools” for this Loop)"
	mcpNoteAllowed = "MCP tool %s: still needs an approval; its server is defined where Loopbee cannot approve it (for Codex: outside your ~/.codex/config.toml, e.g. a project's .codex/config.toml), so set default_tools_approval_mode = \"approve\" for it there"
)

// mcpServerState is one MCP server as the CLI reports it.
type mcpServerState struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// listAgentMCP asks the agent CLI for its MCP servers in dir (at most 20 seconds); ok is false when it cannot say.
func listAgentMCP(ctx context.Context, agent string, base []string, dir string, env []string) ([]mcpServerState, bool) {
	a, ok := adapterFor(agent)
	if !ok || a.ListMCP == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	list, err := a.ListMCP(ctx, base, dir, env)
	return list, err == nil
}

// decodeJSONArray decodes the first JSON array in output (CLIs may print warnings around it).
func decodeJSONArray(output []byte, v any) error {
	i := strings.IndexByte(string(output), '[')
	if i < 0 {
		return json.Unmarshal(output, v)
	}
	return json.NewDecoder(strings.NewReader(string(output[i:]))).Decode(v)
}
