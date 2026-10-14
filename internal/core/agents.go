package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// Agent is a locally installed agent CLI that Loopbee can drive headlessly.
type Agent struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Path    string   `json:"path"`
	Version string   `json:"version"`
	Status  string   `json:"status"`            // available, unverified, unavailable
	Problem string   `json:"problem,omitempty"` // why it is not available, when Loopbee found it but could not use it
	Caps    []string `json:"caps,omitempty"`
	// Levels are the permission levels this CLI enforces; Enforcement and Flags say how, per level.
	Levels      []string          `json:"levels"`
	Enforcement map[string]string `json:"enforcement,omitempty"`
	Flags       map[string]string `json:"flags,omitempty"`
	// MCP lists the MCP servers configured for this agent (user level); MCPFlags how a Loop allows or disables them.
	MCP      []string          `json:"mcp,omitempty"`
	MCPFlags map[string]string `json:"mcpFlags,omitempty"`
}

// Permission levels, lowest to highest. A Loop may not exceed the device ceiling (Settings.MaxSandbox), and the
// agent must enforce the level itself (adapter.Levels).
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
	a, ok := adapterFor(name)
	if !ok {
		return nil, errors.New("unknown agent " + name)
	}
	// LOOPBEE_<NAME>_BIN points at a specific executable (or a .js/.mjs/.cjs entry point).
	if custom := os.Getenv("LOOPBEE_" + strings.ToUpper(name) + "_BIN"); custom != "" {
		if _, err := os.Stat(custom); err != nil {
			return nil, err
		}
		if strings.HasSuffix(custom, ".js") || strings.HasSuffix(custom, ".mjs") || strings.HasSuffix(custom, ".cjs") {
			node, err := exec.LookPath("node")
			if err != nil {
				return nil, err
			}
			return []string{node, custom}, nil
		}
		return []string{custom}, nil
	}
	p, err := exec.LookPath(a.Binary)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" && (strings.HasSuffix(p, ".cmd") || strings.HasSuffix(p, ".ps1")) {
		for _, entry := range a.NpmEntries {
			script := filepath.Join(filepath.Dir(p), "node_modules", filepath.FromSlash(entry))
			if _, err := os.Stat(script); err != nil {
				continue
			}
			node, err := exec.LookPath("node")
			if err != nil {
				return nil, err
			}
			return []string{node, script}, nil
		}
		return nil, errors.New(a.Label + " was found only as a script wrapper; install its native executable or set LOOPBEE_" + strings.ToUpper(name) + "_BIN")
	}
	return []string{p}, nil
}

// DetectAgents probes each supported CLI for its version and headless capabilities.
func (a *App) DetectAgents() {
	out := []Agent{}
	for _, name := range adapterOrder {
		ad := adapters[name]
		ag := Agent{Name: name, Label: ad.Label, Status: "unavailable", Levels: ad.Levels, Enforcement: ad.Enforcement, Flags: ad.Flags, MCPFlags: ad.MCPFlags}
		if home, _ := os.UserHomeDir(); home != "" {
			ag.MCP = agentMCPServers(name, home, "")
		}
		argv, err := agentCommand(name)
		if err == nil {
			ag.Path = argv[len(argv)-1]
			if _, ok := agentOverride[name]; ok {
				ag.Path = argv[0]
			}
			ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
			b, e := runCommand(ctx, "", append(argv, "--version"), nil)
			cancel()
			if e != nil {
				ag.Problem = detectProblem(ad.Binary+" --version", e, b)
			}
			if e == nil {
				ag.Version = strings.TrimSpace(string(b))
				ag.Status = "unverified"
				ctx, cancel = context.WithTimeout(a.ctx, 8*time.Second)
				help, e := runCommand(ctx, "", append(argv, ad.HelpArgs...), nil)
				cancel()
				if e != nil {
					ag.Problem = detectProblem(ad.Binary+" "+strings.Join(ad.HelpArgs, " "), e, help)
				} else {
					ag.Caps = ad.Caps(help)
					if hasCap(ag.Caps, "headless") {
						ag.Status = "available"
						// The CLI's own list is complete (every layer and plugin); the files are the fallback.
						if home, _ := os.UserHomeDir(); home != "" {
							if list, ok := listAgentMCP(a.ctx, name, argv, home, nil); ok {
								ag.MCP = nil
								for _, s := range list {
									if s.Enabled {
										ag.MCP = append(ag.MCP, s.Name)
									}
								}
							}
						}
					} else {
						ag.Problem = "this version's help lists none of the headless flags Loopbee needs; update " + ad.Label
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

// detectProblem explains why a detection command failed. A process killed at once (exit 137, "signal: killed")
// is usually macOS refusing to run a damaged or unsigned binary.
func detectProblem(cmd string, err error, out []byte) string {
	msg := err.Error()
	if errors.Is(err, context.DeadlineExceeded) {
		return cmd + " did not answer within 8 seconds"
	}
	if strings.Contains(msg, "signal: killed") || strings.Contains(msg, "exit status 137") {
		return cmd + " was killed as soon as it started (" + msg + "); the installation is probably damaged or blocked by macOS — reinstall it and check that it runs in a terminal"
	}
	if first := firstLine(string(out), 160); first != "" {
		return cmd + " failed (" + msg + "): " + first
	}
	return cmd + " failed (" + msg + ")"
}

// helpHas returns a capability helper over a CLI's help text.
func helpHas(help []byte) func(...string) bool {
	return func(words ...string) bool {
		for _, w := range words {
			if !strings.Contains(string(help), w) {
				return false
			}
		}
		return true
	}
}

func hasCap(caps []string, c string) bool { return slices.Contains(caps, c) }
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
	Model    string   // model to use; "" = the CLI's own default
	AddDirs  []string // extra writable directories (e.g. the git dir of a worktree)
	LastFile string   // codex: file for the final message
	Schema   string   // optional JSON schema (file path for codex, inline JSON for claude)
	Scratch  string   // a private folder for files an adapter passes to the CLI (e.g. a policy file)
	// ReadPaths are files and folders outside Dir the agent may read but not change: its own skills, plugins and
	// memory, and the Loop's read paths.
	ReadPaths []string
	// MCP: the Loop lets the agent use (and pre-approves) every MCP tool of its own setup (Loop.AgentMCP).
	MCP bool
	// Home is the user's home folder; Project is the developer's checkout (Dir differs in a worktree).
	Home, Project string
	// MCPList is what the CLI reported with ListMCP (MCPListed: it answered).
	MCPList   []mcpServerState
	MCPListed bool
}

// classifyAgentEvent labels one stdout line of Codex or Claude Code. Unknown or non-JSON lines are kept as agent.raw.
func classifyAgentEvent(line string) (string, bool) {
	if kind, failed := codexClassify(line); kind != "agent.raw" {
		return kind, failed
	}
	return claudeClassify(line)
}

// Result contract appended to every agent step so Loopbee can tell done from no-op or blocked.
const resultMarker = "LOOPBEE_RESULT"

func resultInstructions(lang string) string {
	return "\n\n---\nLoopbee runs this step unattended. Report in " + languageName(lang) + ". " +
		"When you finish, end your final message with exactly one line:\n" + resultMarker +
		` {"status":"done|no_action|blocked|needs_approval","summary":"one or two sentences","links":["URLs of PRs, deployments or reports"]}` +
		"\nUse no_action when there was nothing to do, blocked when you could not proceed (missing access, failing preconditions), and needs_approval when a person must decide before anything else happens. Never include secrets in the summary." +
		"\nIf something you needed was unavailable (a file or folder, a host or service, an environment variable or credential, a command, skill, plugin or MCP tool, a permission), add \"missing\": [\"one short item per thing, naming it exactly\"] to that line, also when you could work around it."
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
	// Missing lists what the agent needed and could not get, so the person can give the Loop access to it.
	Missing []string `json:"missing,omitempty"`
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
	missing := []string{}
	for _, m := range r.Missing {
		m = firstLine(m, 200)
		if m != "" && len(missing) < 10 && !slices.Contains(missing, m) {
			missing = append(missing, m)
		}
	}
	r.Missing = missing
	if len(r.Missing) == 0 {
		r.Missing = nil
	}
	return &r
}

// agentFailure extracts a readable reason from a failed Codex or Claude Code turn.
func agentFailure(output []byte) string {
	if r := claudeFailure(output); r != "" {
		return r
	}
	return codexFailure(output)
}
