package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// adapter is everything loopbee knows about one agent CLI. Adapters are compiled in: loopbee has no plugin host.
// Adding a CLI means one file that registers an adapter, a fake for the tests, and a real smoke run.
//
// Permission levels are a promise to the person who reads the Loop: an adapter lists only the levels its CLI can
// really enforce. A Loop asking for any other level with that agent is refused, never run with less protection.
type adapter struct {
	Name  string // identifier stored in Loop.Agent, e.g. "codex"
	Label string // shown to people, e.g. "Codex CLI"
	// Binary is the executable looked up on PATH (LOOPBEE_<NAME>_BIN overrides it). NpmEntries are scripts below an
	// npm prefix that Windows' .cmd shims start; the first that exists is run with node.
	Binary     string
	NpmEntries []string
	HelpArgs   []string                   // arguments that print the headless help, e.g. ["exec", "--help"]
	Caps       func(help []byte) []string // capabilities read from the help text; "headless" means usable
	// Levels the CLI enforces itself. Enforcement says how, per level, for the settings page and the designer.
	Levels      []string
	Enforcement map[string]string
	Flags       map[string]string // what loopbee passes per level, shown in Settings › Agents
	// Args returns the arguments after the executable and extra environment variables for one turn. The prompt is
	// always written to stdin.
	Args func(inv agentInvocation, caps []string) ([]string, []string)
	// Classify labels one stdout line ("agent.<type>"; unknown lines are kept as agent.raw) and reports whether it
	// says the turn failed.
	Classify func(line string) (kind string, failed bool)
	Final    func(output []byte) (text string, structured map[string]any)
	Failure  func(output []byte) string // the CLI's own reason for a failed turn, "" when none
	Usage    func(output []byte) Usage  // model and tokens the CLI reported for the turn
	// DefaultModel is the model the CLI uses when the Loop names none and its events do not say ("" = unknown).
	DefaultModel func(home string) string
	// Context lists the agent's own skills, plugins, instructions and memory (paths that may not exist).
	Context func(home, project string) []string
	Memory  func(home, project string) string // the project's memory folder, "" when the CLI keeps none per project
	// ToolNote tells the chat designer what the agent can do at each level on this computer.
	ToolNote string
	// MCPServers lists the MCP servers of the agent's own setup (user and project), for the editor and the designer;
	// it may be incomplete. MCPFlags says what loopbee passes when a Loop allows them ("on") and when it does not
	// ("off", below agent-config). Refused lists MCP tool calls the CLI refused because nobody could approve them.
	MCPServers func(home, project string) []string
	MCPFlags   map[string]string
	Refused    func(output []byte) []string
	// ListMCP asks the CLI itself which MCP servers it will start in dir (all configuration layers, plugins
	// included), so the switch touches exactly those; nil when the CLI cannot say.
	ListMCP func(ctx context.Context, base []string, dir string, env []string) ([]mcpServerState, error)
	Explain func(reason, goos string) string // adds how to fix a failure the CLI reported; nil = keep the reason
}

var (
	adapters     = map[string]*adapter{}
	adapterOrder []string
)

func registerAdapter(a *adapter) {
	if _, dup := adapters[a.Name]; dup {
		panic("duplicate agent adapter " + a.Name)
	}
	if !slices.Contains(a.Levels, "agent-config") {
		a.Levels = append(a.Levels, "agent-config")
	}
	adapters[a.Name] = a
	adapterOrder = append(adapterOrder, a.Name)
	// Display and default order: the first available agent becomes the default one.
	slices.SortStableFunc(adapterOrder, func(x, y string) int { return adapterRank(x) - adapterRank(y) })
}

func adapterRank(name string) int {
	if i := slices.Index([]string{"codex", "claude", "opencode", "pi"}, name); i >= 0 {
		return i
	}
	return 100
}

func adapterFor(name string) (*adapter, bool) {
	a, ok := adapters[name]
	return a, ok
}

// AgentNames lists the registered agent CLIs in display order.
func AgentNames() []string { return append([]string{}, adapterOrder...) }

// supportsLevel reports whether the agent enforces the permission level.
func supportsLevel(agent, level string) bool {
	a, ok := adapterFor(agent)
	return ok && slices.Contains(a.Levels, level)
}

// levelError explains a level the agent cannot enforce and what to choose instead.
func levelError(agent, level string) error {
	a, ok := adapterFor(agent)
	if !ok {
		return fmt.Errorf("unknown agent %q; loopbee supports %s", agent, strings.Join(adapterOrder, ", "))
	}
	return fmt.Errorf("%s cannot enforce the %q permission on its own, so loopbee will not run it there; choose %s, or another agent", a.Label, level, strings.Join(a.Levels, " or "))
}

// agentLabel is the display name of an agent ("Codex CLI").
func agentLabel(name string) string {
	if a, ok := adapterFor(name); ok {
		return a.Label
	}
	return name
}

// buildAgentInvocation returns argv and extra environment variables for one turn.
func buildAgentInvocation(base []string, caps []string, inv agentInvocation) ([]string, []string) {
	a, ok := adapterFor(inv.Name)
	if !ok {
		return append([]string{}, base...), nil
	}
	args, env := a.Args(inv, caps)
	return append(append([]string{}, base...), args...), env
}

// buildAgentArgs is buildAgentInvocation without the environment.
func buildAgentArgs(base []string, caps []string, inv agentInvocation) []string {
	argv, _ := buildAgentInvocation(base, caps, inv)
	return argv
}

// classifyFor labels a stdout line of the given agent.
func classifyFor(agent, line string) (string, bool) {
	if a, ok := adapterFor(agent); ok && a.Classify != nil {
		return a.Classify(line)
	}
	return classifyAgentEvent(line)
}

// finalMessage extracts the agent's last assistant message from its event stream.
func finalMessage(name string, output []byte) (string, map[string]any) {
	if a, ok := adapterFor(name); ok && a.Final != nil {
		return a.Final(output)
	}
	return "", nil
}

// failureFor is the CLI's reason for a failed turn, with what loopbee knows about fixing it.
func failureFor(agent string, output []byte, goos string) string {
	a, ok := adapterFor(agent)
	reason := ""
	if ok && a.Failure != nil {
		reason = a.Failure(output)
	} else {
		reason = agentFailure(output)
	}
	if len(reason) > 500 {
		reason = reason[:500] + "…"
	}
	if reason != "" && ok && a.Explain != nil {
		return a.Explain(reason, goos)
	}
	return reason
}

// explainFailure adds what loopbee knows about fixing a failure the CLI reported.
func explainFailure(agent, reason, goos string) string {
	if a, ok := adapterFor(agent); ok && a.Explain != nil {
		return a.Explain(reason, goos)
	}
	return reason
}

// usageFor reads the model and tokens of one turn; model falls back to the Loop's, then the CLI's configured one.
func usageFor(agent string, output []byte, loopModel, home string) *Usage {
	a, ok := adapterFor(agent)
	if !ok || a.Usage == nil {
		return nil
	}
	u := a.Usage(output)
	if u.Model == "" {
		u.Model = loopModel
	}
	if u.Model == "" && a.DefaultModel != nil {
		u.Model = a.DefaultModel(home)
	}
	u.Agent = agent
	if u.empty() && u.Model == "" {
		return nil
	}
	return &u
}

// existing filters paths to those present on this computer.
func existing(paths []string) []string {
	var out []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// agentContextPaths lists the agent's own context that exists on this computer. projectDir is the Loop's project
// (the developer's checkout, also when the Run works in a worktree).
func agentContextPaths(agent, home, projectDir string) []string {
	a, ok := adapterFor(agent)
	if home == "" || !ok || a.Context == nil {
		return nil
	}
	return existing(a.Context(home, projectDir))
}

// agentMemoryDir is where the agent keeps memory for the project in dir ("" when the agent has none there).
func agentMemoryDir(agent, home, dir string) string {
	if a, ok := adapterFor(agent); ok && a.Memory != nil && dir != "" {
		return a.Memory(home, dir)
	}
	return ""
}

// envHome returns the directory named by an environment variable, else filepath.Join(home, rel...).
func envHome(env, home string, rel ...string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	return filepath.Join(append([]string{home}, rel...)...)
}

// jsonLines decodes every JSON object line of a CLI's stdout.
func jsonLines(output []byte, each func(line string, ev map[string]any)) {
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) == nil {
			each(line, ev)
		}
	}
}

// Small accessors for decoded JSON.
func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func obj(m map[string]any, k string) map[string]any {
	o, _ := m[k].(map[string]any)
	return o
}
func num(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}
func fnum(m map[string]any, k string) float64 { f, _ := m[k].(float64); return f }

// Usage is the model and tokens of one agent turn or, summed, of a Run. Input counts prompt tokens that were not
// read from the provider's cache; CacheRead and CacheWrite count cached prompt tokens; Output counts generated tokens
// including reasoning (Reasoning repeats the reasoning part when the CLI reports it). CostUSD is the CLI's own
// figure; loopbee never estimates prices.
type Usage struct {
	Agent      string  `json:"agent,omitempty"`
	Model      string  `json:"model,omitempty"`
	Input      int64   `json:"input"`
	CacheRead  int64   `json:"cacheRead,omitempty"`
	CacheWrite int64   `json:"cacheWrite,omitempty"`
	Output     int64   `json:"output"`
	Reasoning  int64   `json:"reasoning,omitempty"`
	CostUSD    float64 `json:"costUsd,omitempty"`
}

func (u Usage) empty() bool {
	return u.Input == 0 && u.CacheRead == 0 && u.CacheWrite == 0 && u.Output == 0 && u.CostUSD == 0
}

// Total is every token the turn used.
func (u Usage) Total() int64 { return u.Input + u.CacheRead + u.CacheWrite + u.Output }

// add sums b into u; different agents or models are listed together.
func (u *Usage) add(b Usage) {
	u.Input += b.Input
	u.CacheRead += b.CacheRead
	u.CacheWrite += b.CacheWrite
	u.Output += b.Output
	u.Reasoning += b.Reasoning
	u.CostUSD += b.CostUSD
	u.Agent = joinUnique(u.Agent, b.Agent)
	u.Model = joinUnique(u.Model, b.Model)
}

func joinUnique(list, item string) string {
	if item == "" {
		return list
	}
	parts := []string{}
	if list != "" {
		parts = strings.Split(list, ", ")
	}
	for _, it := range strings.Split(item, ", ") {
		if !slices.Contains(parts, it) {
			parts = append(parts, it)
		}
	}
	return strings.Join(parts, ", ")
}
