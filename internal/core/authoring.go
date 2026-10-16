package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AuthorRequest is one turn of the "describe your Loop" conversation.
type AuthorRequest struct {
	Agent     string        `json:"agent"`
	Directory string        `json:"directory"`
	Timezone  string        `json:"timezone,omitempty"` // the user's IANA time zone, used for a new draft
	Messages  []ChatMessage `json:"messages"`
	Draft     *Loop         `json:"draft,omitempty"`
}

// AuthorQuestion is a clarifying question with quick-reply options.
type AuthorQuestion struct {
	Text    string   `json:"text"`
	Options []string `json:"options"`
}

// AuthorReply is the agent's proposal. Loop is always a disabled draft that still goes through Validate.
type AuthorReply struct {
	Reply     string           `json:"reply"`
	Questions []AuthorQuestion `json:"questions"`
	Loop      Loop             `json:"loop"`
	Problem   string           `json:"problem,omitempty"`
	Agent     string           `json:"agent"`
}

// AuthorSchema returns the JSON schema of an authoring answer (used by beectl schema).
func AuthorSchema() map[string]any { return authorSchema() }

// authorSchema is the structured-output contract. Every property is required so it also satisfies
// strict JSON-schema modes; empty strings and arrays mean "not set".
func authorSchema() map[string]any {
	str := map[string]any{"type": "string"}
	strs := map[string]any{"type": "array", "items": str}
	integer := map[string]any{"type": "integer"}
	obj := func(props map[string]any) map[string]any {
		req := make([]string, 0, len(props))
		for k := range props {
			req = append(req, k)
		}
		return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": req}
	}
	step := obj(map[string]any{
		"kind": map[string]any{"type": "string", "enum": []string{"act", "observe", "summarize", "run", "verify", "gate"}},
		"name": str, "prompt": str, "argv": strs, "metric": str, "metricPath": str, "continueOnError": map[string]any{"type": "boolean"},
	})
	loop := obj(map[string]any{
		"name": str, "description": str, "directory": str, "cron": str, "timezone": str,
		"sandbox":   map[string]any{"type": "string", "enum": []string{"read-only", "workspace-write", "workspace-network", "agent-config"}},
		"workspace": map[string]any{"type": "string", "enum": []string{"direct", "worktree"}},
		"baseRef":   str, "branch": str, "lock": str, "maxRounds": integer, "wallMinutes": integer, "stallRounds": integer, "gateHours": integer,
		"doneWhen": str, "allowedTools": strs,
		"steps": map[string]any{"type": "array", "items": step},
		"notify": obj(map[string]any{"events": strs, "includeReport": map[string]any{"type": "boolean"},
			"channels": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"desktop", "telegram", "feishu", "bark"}}}}),
		// Strict schemas have no optional objects: an empty kind / mode means "not used".
		"intake":      obj(map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"", "github-issues"}}, "repo": str, "label": str, "trustedUsers": strs}),
		"pullRequest": obj(map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"", "after-review", "draft"}}, "base": str, "merge": map[string]any{"type": "string", "enum": []string{"", "after-checks"}}}),
		"env":         map[string]any{"type": "array", "items": obj(map[string]any{"name": str, "secret": map[string]any{"type": "boolean"}, "value": str})},
		"readPaths":   strs, "copyFiles": strs, "model": str, "agentMcp": map[string]any{"type": "boolean"},
	})
	return obj(map[string]any{
		"reply":     str,
		"questions": map[string]any{"type": "array", "items": obj(map[string]any{"text": str, "options": strs})},
		"loop":      loop,
	})
}

// agentToolNote tells the designer what the agent can do, and which permission levels it enforces.
func agentToolNote(agent string) string {
	a, ok := adapterFor(agent)
	if !ok {
		return ""
	}
	return a.ToolNote + "\n  " + a.Label + " enforces these permission levels: " + strings.Join(a.Levels, ", ") +
		". Never choose another level for it: loopbee refuses to run it there."
}

func authorPrompt(req AuthorRequest, dir, lang, tz, agent, maxSandbox string, draft Loop) string {
	d, _ := json.MarshalIndent(definitionForAgent(draft), "", "  ")
	var conv strings.Builder
	for _, m := range req.Messages {
		fmt.Fprintf(&conv, "[%s] %s\n", m.Role, m.Text)
	}
	return `You are helping a developer configure loopbee, a local desktop scheduler that runs coding agents
(` + agentLabels() + `) headlessly on a schedule, records evidence, pauses at human gates and sends
notifications. You are NOT running the workflow now: you only design the Loop definition.

Working directory: ` + dir + `
` + folderNote(req) + `You may READ this directory (read-only) to ground the definition: AGENTS.md, CLAUDE.md, README, skills or
commands under .codex/ .claude/ .agents/, package.json scripts, Makefile, deploy scripts. Refer to the
team's existing skills, slash commands and scripts by their real names. Do not modify anything.

Design rules:
- Teams already have complete development and verification workflows in their agents. Prefer ONE agent
  step (kind "act") whose prompt says which skill/workflow to use, what to do, and when to stop.
- Put a "gate" step before anything irreversible or shared: merging to the main branch, production deploys,
  sending e-mail to customers, running SQL on shared databases — unless the user says otherwise. A gate as
  the last step means a person takes over from there.
- Use "run" steps only for deterministic scripts (argv array, no shell; argv[0] must be in allowedTools).
  Use "verify" only for commands whose exit code decides whether another round is needed.
- run/verify steps execute as the user, outside any sandbox. When the Loop acts on input others can write (issues,
  e-mail, web pages, logs), put no run/verify step between the agent step and a gate: they would execute code the
  agent changed before anyone reviewed it; rely on the agent's own sandboxed tests and on CI after the gate.
- Work from GitHub issues when the user asks for it (“work through our issues”, “fix issues labelled bug”): set
  intake {kind "github-issues", label (empty = agent-ready), repo only when it is not this folder's origin,
  trustedUsers only for logins the user names}. Each Run loopbee takes one issue opened by someone with write
  access, labels it agent-working and gives it to the agent; no issue means “nothing to do” without an agent call.
  Use workspace "worktree", branch "loopbee/issue-{issue}", baseRef "origin/<default branch>", and one agent step
  whose prompt says to follow the repository's AGENTS.md and lessons if it has them. A schedule such as hourly on
  weekdays suits it.
- Pull requests are loopbee's job: set pullRequest.mode "after-review" (default; put a gate after the agent step —
  the person reviews the diff in loopbee, then loopbee pushes and opens the pull request) or "draft" (no gate; a
  draft pull request is the review, e.g. when the user wants to review on GitHub or the phone). Never put push or
  pull-request commands in steps or prompts. Leave mode empty when a branch is enough. When the user wants the
  reviewed change merged without merging by hand, set pullRequest.merge "after-checks" (only with "after-review"):
  loopbee waits for the pull request's checks and squash-merges it when all pass, otherwise leaves it open.
- notify.channels: the channels the user names (telegram, feishu, bark, desktop); empty means all configured.
- loopbee hands the output of earlier run/verify steps to later agent steps of the same round. Set
  continueOnError=true on a run step when a non-zero exit is information, not a failure (e.g. failing tests
  that the agent should report on).
- This Loop will run with ` + agentLabel(agent) + `. ` + agentToolNote(agent) + `
- This computer runs ` + platformName() + `.
- The agent keeps its own context in every run: the user's skills, plugins, subagents, slash commands,
  CLAUDE.md / AGENTS.md and the project's memory. Name the skills to use. A skill that runs scripts or commands
  needs at least workspace-write; one that calls services (APIs, databases, deployments, git push, e-mail) needs
  workspace-network. Choose the level the workflow really needs: a run that cannot reach what it needs fails.
- readPaths: folders or files outside the project that the workflow reads, e.g. a skill's credentials or config
  folder (“~/.pi/agent/private”). Read-only for the agent. Never ~/.ssh, ~/.aws or similar credential stores.
- copyFiles (worktree only): Git-ignored files of the project the work needs from the developer's checkout
  (.env, CLAUDE.local.md, local config). A fresh worktree has no ignored files and no installed dependencies:
  install those with a run step before the agent step (e.g. ["npm","ci"]).
- env: variables the workflow needs. value "env:NAME" passes the variable NAME from the user's shell
  (loopbee also looks in the login shell); otherwise leave value empty and the user fills it in loopbee.
- model: leave empty (the agent's own default) unless the user names a model for this Loop.
- agentMcp: true when the work uses the agent's own MCP tools: a browser (to operate a web console, test a site,
  fill a form), a database, a ticket system, a design tool or anything the user does through their agent's
  connected tools. Nobody can approve a tool in an unattended run, so without agentMcp every MCP tool is
  disabled (except at agent-config, where the agent's own settings decide). MCP tools run outside the sandbox
  with the user's rights: when they change something (a console, a database), put a gate before that step or
  split the work into a read step, a gate and an apply step. A browser started from a shell command does not
  start inside the macOS sandbox; the agent's browser MCP tool does.
- Permission ("sandbox"): the lowest that works. read-only; workspace-write (edit files, no network);
  workspace-network (git push, deployments, databases, HTTP APIs, e-mail); agent-config only if the user
  explicitly wants the agent's own configuration to decide. For ` + agentLabel(agent) + ` ONLY these are possible: ` +
		strings.Join(agentLevelsOf(agent), ", ") + `. run steps execute outside the agent, so they never need a higher level.
- When the Loop changes code, use workspace "worktree" with baseRef (e.g. "origin/master") and branch (e.g.
  "loopbee/fix-{date}"; {date}, {time}, {run} and {issue} are filled per run). loopbee fetches baseRef itself (this
  never requires network permission), creates the branch in a fresh worktree and commits the agent's changes after
  each agent step. Agent prompts must therefore NOT create branches, commit or push (agents have no Git access).
  Pull requests come from pullRequest (below). Merging to the main branch or deploying to production is done by the
  agent only if the user asks for it (needs workspace-network); otherwise a person does it after the gate.
  Read-only Loops use "direct" without a branch.
- This device allows at most "` + maxSandbox + `". Stay within it. If the task cannot work without more (for
  example it must push, deploy or call an API), keep the level it needs and say in "reply" that the device
  limit must be raised in Settings › General.
- Schedule: five-field cron in timezone ` + tz + ` (keep the user's timezone if given); empty cron = manual only.
- One agent step => maxRounds 1, doneWhen "verify.passed". Typical wallMinutes 30–120. gateHours default 72.
- Never write secrets. If credentials are needed, list env entries with secret=true; the user fills values later.
- notify.events from done, attention, failed, no_action. Use includeReport=true for report/digest Loops.
- Keep fields you are not changing exactly as in the current draft.
- If essential information is missing, ask at most 3 short questions, each with 2–4 short options. Still return
  your best current draft.
- Write "reply", "questions" and step names in ` + languageName(lang) + `. Prompts for agents may be in any
  language the team uses.

Current draft:
` + string(d) + `

Conversation:
` + conv.String() + `
Return ONLY one JSON object: {"reply": string, "questions": [{"text","options"}], "loop": {...}} matching the schema.`
}

// definitionForAgent strips runtime-only fields from a draft.
func definitionForAgent(l Loop) map[string]any {
	intake := map[string]any{"kind": "", "repo": "", "label": "", "trustedUsers": []string{}}
	if l.Intake != nil {
		intake = map[string]any{"kind": l.Intake.Kind, "repo": l.Intake.Repo, "label": l.Intake.Label, "trustedUsers": nonNil(l.Intake.TrustedUsers)}
	}
	pr := map[string]any{"mode": "", "base": "", "merge": ""}
	if l.PullRequest != nil {
		pr = map[string]any{"mode": l.PullRequest.Mode, "base": l.PullRequest.Base, "merge": l.PullRequest.Merge}
	}
	steps := []map[string]any{}
	for _, s := range l.Steps {
		steps = append(steps, map[string]any{"kind": s.Kind, "name": s.Name, "prompt": s.Prompt, "argv": nonNil(s.Argv), "metric": s.Metric, "metricPath": s.MetricPath, "continueOnError": s.ContinueOnError})
	}
	env := []map[string]any{}
	for _, e := range l.Env {
		value := "" // never show stored values to the agent; references to the user's shell are not secret
		if isEnvReference(e.Value) {
			value = e.Value
		}
		env = append(env, map[string]any{"name": e.Name, "secret": e.Secret, "value": value})
	}
	return map[string]any{
		"name": l.Name, "description": l.Description, "cron": l.Cron, "timezone": l.Timezone, "sandbox": l.Sandbox,
		"workspace": orDefault(l.Workspace, "direct"), "baseRef": l.BaseRef, "branch": l.Branch, "lock": l.Lock, "maxRounds": l.MaxRounds,
		"wallMinutes": l.WallMinutes, "stallRounds": l.StallRounds, "gateHours": l.GateHours, "doneWhen": l.DoneWhen,
		"allowedTools": nonNil(l.AllowedTools), "steps": steps,
		"notify": map[string]any{"events": nonNil(l.Notify.Events), "includeReport": l.Notify.IncludeReport, "channels": nonNil(l.Notify.Channels)}, "env": env,
		"intake": intake, "pullRequest": pr, "readPaths": nonNil(l.ReadPaths), "copyFiles": nonNil(l.CopyFiles), "model": l.Model, "agentMcp": l.AgentMCP,
	}
}
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// Author asks the bound agent CLI, in read-only mode inside the target directory, to propose a Loop.
func (a *App) Author(ctx context.Context, req AuthorRequest) (AuthorReply, error) {
	c := a.Settings()
	if c.Role != "executor" {
		return AuthorReply{}, errors.New("viewer is read-only")
	}
	if len(req.Messages) == 0 || strings.TrimSpace(req.Messages[len(req.Messages)-1].Text) == "" {
		return AuthorReply{}, errors.New("describe the Loop first")
	}
	if len(req.Messages) > 40 {
		req.Messages = req.Messages[len(req.Messages)-40:]
	}
	name := req.Agent
	if name == "" {
		name = c.DefaultAgent
	}
	ag, ok := a.agentInfo(name)
	if !ok || ag.Status != "available" {
		return AuthorReply{}, fmt.Errorf("agent %s is not available; run detection in Settings › Agents", orDefault(name, "(none)"))
	}
	base, err := agentCommand(name)
	if err != nil {
		return AuthorReply{}, err
	}
	scratch, err := os.MkdirTemp(filepath.Join(a.Dir), "author-")
	if err != nil {
		return AuthorReply{}, err
	}
	defer os.RemoveAll(scratch)
	dir := req.Directory
	if info, err := os.Stat(dir); dir == "" || !filepath.IsAbs(dir) || err != nil || !info.IsDir() {
		dir = scratch
	}
	draft := defaultDraft(c, name)
	if _, err := time.LoadLocation(req.Timezone); req.Timezone != "" && err == nil {
		draft.Timezone = req.Timezone
	}
	if req.Draft != nil {
		draft = *req.Draft
	}
	if req.Directory != "" {
		draft.Directory = req.Directory
	}
	schema, _ := json.Marshal(authorSchema())
	// The designer may read the user's own skills, plugins and memory too, so it refers to them by their real names.
	home, _ := os.UserHomeDir()
	agentCtx := agentContextPaths(name, home, draft.Directory)
	inv := agentInvocation{Name: name, Dir: dir, Sandbox: "read-only", ReadPaths: agentCtx, Scratch: scratch, Home: home, Project: draft.Directory}
	if name == "codex" {
		schemaFile := filepath.Join(scratch, "schema.json")
		if err := os.WriteFile(schemaFile, schema, 0600); err != nil {
			return AuthorReply{}, err
		}
		inv.Schema = schemaFile
		inv.LastFile = filepath.Join(scratch, "last.json")
	} else {
		inv.Schema = string(schema)
	}
	inv.MCPList, inv.MCPListed = listAgentMCP(ctx, name, base, dir, nil) // the designer gets no MCP tools
	argv, agentEnv := buildAgentInvocation(base, ag.Caps, inv)
	tz := draft.Timezone
	if tz == "" {
		tz = c.DigestTimezone
	}
	prompt := authorPrompt(req, dir, c.Language, tz, name, c.MaxSandbox, draft) + designerContext(agentCtx) + designerMCP(name, home, draft.Directory)
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	out, runErr := runCommandEnv(ctx, dir, argv, nil, strings.NewReader(prompt), agentEnv)
	text := ""
	if inv.LastFile != "" {
		if b, err := os.ReadFile(inv.LastFile); err == nil {
			text = string(b)
		}
	}
	var structured map[string]any
	if strings.TrimSpace(text) == "" {
		text, structured = finalMessage(name, out)
	}
	var reply AuthorReply
	if structured != nil {
		b, _ := json.Marshal(structured)
		err = json.Unmarshal(b, &reply)
	} else {
		err = decodeFirstObject(text, &reply)
	}
	if err != nil {
		if ctx.Err() != nil {
			return AuthorReply{}, errors.New("the agent did not answer within 4 minutes")
		}
		if reason := failureFor(name, out, runtime.GOOS); reason != "" {
			return AuthorReply{}, errors.New("agent failed: " + reason)
		}
		if runErr != nil {
			return AuthorReply{}, fmt.Errorf("agent failed: %v: %s", runErr, tail(string(out), 400))
		}
		return AuthorReply{}, errors.New("the agent's answer was not a Loop definition; try rephrasing")
	}
	reply.Agent = name
	reply.Loop = normalizeDraft(reply.Loop, draft, name)
	// A designer sometimes asks for a level its own CLI cannot enforce (seen with pi). Lower it, never raise it,
	// and say so: commands can still run as run steps before the agent step.
	if from := reply.Loop.Sandbox; validSandbox(from) && !supportsLevel(name, from) {
		reply.Loop.Sandbox = fallbackLevel(name, from)
		reply.Reply = strings.TrimSpace(reply.Reply + "\n\n" + levelNote(c.Language, agentLabel(name), from, reply.Loop.Sandbox))
	}
	if err := Validate(reply.Loop); err != nil {
		reply.Problem = err.Error()
	}
	if !sandboxWithin(reply.Loop.Sandbox, c.MaxSandbox) {
		reply.Problem = "permission exceeds this device's limit"
	}
	return reply, nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// decodeFirstObject reads the first JSON object in text (tolerates code fences or prose around it).
func decodeFirstObject(text string, v any) error {
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		if json.NewDecoder(strings.NewReader(text[i:])).Decode(v) == nil {
			return nil
		}
	}
	return errors.New("no JSON object")
}

func defaultDraft(c Settings, agent string) Loop {
	return Loop{Agent: agent, Sandbox: "read-only", Timezone: c.DigestTimezone, CatchUp: true, Private: true, RequireClean: true,
		DoneWhen: "verify.passed", MaxRounds: 1, WallMinutes: 60, StallRounds: 2, GateHours: 72, Workspace: "direct",
		AllowedTools: []string{}, Steps: []Step{}}
}

// normalizeDraft keeps identity and safety-relevant fields under loopbee's control.
func normalizeDraft(p, base Loop, agent string) Loop {
	p.ID, p.Version, p.Next, p.Updated = base.ID, base.Version, base.Next, base.Updated
	// A chosen folder wins; without one, the folder the user named in the chat, if it exists.
	proposed := p.Directory
	p.Directory, p.Project = base.Directory, base.Project
	if base.Directory == "" {
		p.Directory = proposedDirectory(proposed)
	}
	p.Agent = agent
	p.Enabled = base.Enabled && base.ID != "" // a proposal never enables a new Loop
	p.Private = base.Private || base.ID == ""
	p.CatchUp = base.CatchUp
	p.Conversation = base.Conversation
	p.Source = orDefault(base.Source, "chat")
	if p.Workspace == "" {
		p.Workspace = "direct"
	}
	p.RequireClean = p.Workspace == "direct" && base.RequireClean && (p.Directory == "" || gitWorkTree(p.Directory))
	if p.Workspace != "worktree" {
		p.Branch = ""
	}
	if p.Timezone == "" {
		p.Timezone = base.Timezone
	}
	if p.MaxRounds == 0 {
		p.MaxRounds = 1
	}
	if p.WallMinutes == 0 {
		p.WallMinutes = 60
	}
	if p.StallRounds == 0 {
		p.StallRounds = 2
	}
	if p.GateHours == 0 {
		p.GateHours = 72
	}
	if p.DoneWhen == "" {
		p.DoneWhen = "verify.passed"
	}
	if p.AllowedTools == nil {
		p.AllowedTools = []string{}
	}
	for _, s := range p.Steps {
		if (s.Kind == "run" || s.Kind == "verify") && len(s.Argv) > 0 && !contains(p.AllowedTools, s.Argv[0]) {
			p.AllowedTools = append(p.AllowedTools, s.Argv[0])
		}
	}
	for i := range p.Steps {
		if p.Steps[i].Kind != "run" && p.Steps[i].Kind != "verify" {
			p.Steps[i].Argv = nil
		}
		// A metric is optional and needs both its name and its JSON path; real Codex proposed a verify step with
		// only one of them, and the draft could not be saved.
		if p.Steps[i].Kind != "verify" || p.Steps[i].Metric == "" || p.Steps[i].MetricPath == "" {
			p.Steps[i].Metric, p.Steps[i].MetricPath = "", ""
		}
		if p.Steps[i].Kind != "run" {
			p.Steps[i].ContinueOnError = false
		}
		if p.Steps[i].Kind == "run" || p.Steps[i].Kind == "verify" || p.Steps[i].Kind == "gate" {
			p.Steps[i].Prompt = "" // only agent steps have prompts
		}
	}
	if p.Intake != nil && p.Intake.Kind == "" {
		p.Intake = nil
	}
	if p.PullRequest != nil && p.PullRequest.Mode == "" {
		p.PullRequest = nil
	}
	if p.Intake != nil {
		p.Intake.Label = strings.TrimSpace(p.Intake.Label)
		if p.Intake.Label == "agent-ready" {
			p.Intake.Label = "" // the default
		}
	}
	// Keep values already stored for this Loop; from the agent accept only references to the user's shell
	// ("env:NAME"), never a value.
	old := map[string]EnvVar{}
	for _, e := range base.Env {
		old[e.Name] = e
	}
	for i, e := range p.Env {
		proposed := e.Value
		p.Env[i].Value = ""
		if o, ok := old[e.Name]; ok {
			p.Env[i].Value = o.Value
		}
		if isEnvReference(proposed) && (p.Env[i].Value == "" || isEnvReference(p.Env[i].Value)) {
			p.Env[i].Value = proposed
		}
	}
	if p.Workspace != "worktree" {
		p.CopyFiles = nil
	}
	return p
}
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// isEnvReference reports whether v is "env:NAME", a reference to a variable of the user's environment.
func isEnvReference(v string) bool {
	name, ok := strings.CutPrefix(v, "env:")
	return ok && envName.MatchString(name)
}

func platformName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	}
	return runtime.GOOS
}

// designerContext tells the designer where the user's own skills, plugins and memory are.
func designerContext(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return "\n\nYou may also read the user's own agent context, which every run of this Loop has: " + strings.Join(paths, ", ") +
		". Look there for the skills, plugins, commands and memory the workflow relies on and refer to them by name."
}

// designerMCP names the MCP servers of the agent's own setup, so the designer knows what agentMcp would give.
func designerMCP(agent, home, project string) string {
	names := agentMCPServers(agent, home, project)
	if len(names) == 0 {
		return ""
	}
	return "\n\nThe user's " + agentLabel(agent) + " has these MCP servers: " + strings.Join(names, ", ") +
		". If the workflow needs one of them, set agentMcp true and say in \"reply\" which one it will use."
}

// agentLabels lists the supported agent CLIs for prompts ("Codex CLI, Claude Code, …").
func agentLabels() string {
	labels := []string{}
	for _, n := range adapterOrder {
		labels = append(labels, adapters[n].Label)
	}
	return strings.Join(labels, ", ")
}

func agentLevelsOf(agent string) []string {
	if a, ok := adapterFor(agent); ok {
		return a.Levels
	}
	return nil
}

// fallbackLevel is the highest level below from that the agent enforces (never agent-config, never higher).
func fallbackLevel(agent, from string) string {
	best := "read-only"
	for _, l := range agentLevelsOf(agent) {
		if l != "agent-config" && sandboxRank[l] < sandboxRank[from] && sandboxRank[l] > sandboxRank[best] {
			best = l
		}
	}
	return best
}

func levelNote(lang, label, from, to string) string {
	switch lang {
	case "zh-TW":
		return fmt.Sprintf("（loopbee：%s 無法強制執行「%s」，草稿已改為「%s」。需要的指令請放在代理步驟之前的指令步驟。）", label, from, to)
	case "zh-CN":
		return fmt.Sprintf("（loopbee：%s 无法强制执行“%s”，草稿已改为“%s”。需要的命令请放在代理步骤之前的命令步骤。）", label, from, to)
	}
	return fmt.Sprintf("(loopbee: %s cannot enforce %q, so the draft now uses %q. Put the commands it needs in run steps before the agent step.)", label, from, to)
}

// proposedDirectory is a folder the designer proposed, kept only when it is an absolute folder that exists.
func proposedDirectory(dir string) string {
	dir = strings.TrimSpace(dir)
	if info, err := os.Stat(dir); dir == "" || !filepath.IsAbs(dir) || err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Clean(dir)
}

// gitWorkTree reports whether dir is inside a Git working tree; a folder holding several repositories is not.
func gitWorkTree(dir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runCommand(ctx, dir, []string{"git", "rev-parse", "--is-inside-work-tree"}, nil)
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// folderNote tells the designer whether the person chose the project folder or it may propose the one they name.
func folderNote(req AuthorRequest) string {
	if req.Directory != "" {
		return "This is the project folder the person chose; leave loop.directory \"\".\n"
	}
	return "No project folder was chosen, so this is an empty scratch folder. If the person names the folder the Loop works\n" +
		"in, put its absolute path in loop.directory (loopbee checks that it exists); otherwise leave it \"\".\n"
}
