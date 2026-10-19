package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

const Brand = "loopbee"

// keyringService names the keychain entries Loopbee stored before it was renamed. macOS compares it case-sensitively,
// so it stays capitalised: changing it would hide every saved secret.
const keyringService = "Loopbee"

// Version is overridden at build time with -ldflags "-X loopbee/internal/core.Version=…".
var Version = "0.1.0-dev"

type Step struct {
	ID          string       `json:"id,omitempty"`
	Agent       string       `json:"agent,omitempty"`
	Sandbox     string       `json:"sandbox,omitempty"`
	Inputs      []string     `json:"inputs,omitempty"`
	SendBackTo  string       `json:"sendBackTo,omitempty"`
	MaxAttempts int          `json:"maxAttempts,omitempty"`
	PullRequest *PullRequest `json:"pullRequest,omitempty"`
	// Kind: observe/act/summarize (agent), verify (allowlisted command; failure starts the next round),
	// run (allowlisted command; failure fails the Run unless ContinueOnError), gate (human decision).
	// The output of run/verify steps is passed to later agent steps of the same round.
	Metric     string   `json:"metric,omitempty"`
	MetricPath string   `json:"metricPath,omitempty"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Prompt     string   `json:"prompt,omitempty"`
	Argv       []string `json:"argv,omitempty"`
	// ContinueOnError (run steps only): a non-zero exit is information for later steps, not a failure.
	ContinueOnError bool `json:"continueOnError,omitempty"`
	// Model and Effort (agent steps only): override the Loop's model and effort; empty = inherit from Loop.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}
type Loop struct {
	DoneWhen    string `json:"doneWhen"`
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Directory   string `json:"directory"`
	// Project groups Loops on the boards; published with the team snapshot (Directory is not). Empty on save: the
	// owner/repo of the directory's origin remote, or else its folder name.
	Project      string    `json:"project,omitempty"`
	Agent        string    `json:"agent"`
	Model        string    `json:"model,omitempty"`  // model the agent CLI uses; "" = the CLI's own default
	Effort       string    `json:"effort,omitempty"` // thinking/reasoning effort level; "" = the CLI's own default
	Sandbox      string    `json:"sandbox"`
	Cron         string    `json:"cron"`
	Timezone     string    `json:"timezone"`
	CatchUp      bool      `json:"catchUp"`
	Enabled      bool      `json:"enabled"`
	Private      bool      `json:"private"`
	RequireClean bool      `json:"requireClean"`
	Lock         string    `json:"lock"`
	MaxRounds    int       `json:"maxRounds"`
	WallMinutes  int       `json:"wallMinutes"`
	StallRounds  int       `json:"stallRounds"`
	AllowedTools []string  `json:"allowedTools"`
	Steps        []Step    `json:"steps"`
	Next         time.Time `json:"next"`
	Updated      time.Time `json:"updated"`
	// Workspace is "direct" (run in Directory) or "worktree" (a fresh git worktree per Run).
	Workspace string `json:"workspace,omitempty"`
	BaseRef   string `json:"baseRef,omitempty"` // worktree base, e.g. origin/master; empty = HEAD
	// Branch (worktree only): loopbee creates this branch for the Run and commits the agent's changes on it
	// after each agent step, so agents never write Git metadata (Codex's sandbox forbids it).
	// {date}, {time}, {run} and {issue} are replaced per Run.
	Branch string `json:"branch,omitempty"`
	// Intake: where a Run's work item comes from (nil = the prompt alone). PullRequest: what loopbee does with the
	// branch when the Run succeeds. Both are loopbee's job, not the project's, so any repository can use them and
	// an agent can never change how work is picked or delivered.
	Intake      *Intake      `json:"intake,omitempty"`
	PullRequest *PullRequest `json:"pullRequest,omitempty"`
	// ReadPaths are folders or files outside the project the agent may read but not change, e.g. a skill's
	// credentials folder ("~/" = the user's home). The agent's own skills, plugins and memory are always readable.
	ReadPaths []string `json:"readPaths,omitempty"`
	// CopyFiles (worktree only) are Git-ignored files or folders of the project, such as .env or CLAUDE.local.md,
	// that loopbee copies from the developer's checkout into each fresh worktree. They are never committed.
	CopyFiles []string `json:"copyFiles,omitempty"`
	// AgentMCP lets the agent use every MCP tool of its own setup (a browser, a database, a console …) and approves
	// them in advance, since nobody can answer an approval prompt in an unattended Run. MCP tools run outside the
	// agent's sandbox with the user's rights. Off: loopbee disables them, except at agent-config, where the agent's
	// own configuration decides.
	AgentMCP bool `json:"agentMcp,omitempty"`
	// GateHours is how long a gate may wait for a decision; waiting does not consume WallMinutes.
	GateHours    int           `json:"gateHours,omitempty"`
	Env          []EnvVar      `json:"env,omitempty"`
	Notify       NotifyRule    `json:"notify,omitempty"`
	Conversation []ChatMessage `json:"conversation,omitempty"` // authoring chat that produced this definition
	Source       string        `json:"source,omitempty"`       // where the definition came from (URL, template, chat)
}

// Intake picks one work item per Run. Kind "github-issues": the oldest open issue with Label, opened by a trusted
// person (repository OWNER/MEMBER/COLLABORATOR or a listed login), that nobody is working on. loopbee labels it
// WorkingLabel and gives it to the agent as untrusted text; no issue ends the Run as "nothing to do".
type Intake struct {
	Kind         string   `json:"kind"`
	Repo         string   `json:"repo,omitempty"`         // owner/name; empty = the repository of the working directory
	Label        string   `json:"label,omitempty"`        // default agent-ready
	WorkingLabel string   `json:"workingLabel,omitempty"` // default agent-working
	TrustedUsers []string `json:"trustedUsers,omitempty"` // logins trusted in addition to people with write access
}

// PullRequest delivers the Run's branch: Mode "after-review" opens a pull request when the Run completes, which
// requires a gate after the agent; "draft" opens a draft pull request without a gate in loopbee (the draft is the
// review). Changes to .github/ are never pushed automatically.
// Mode "automatic" is deliver-only, requires an explicit test base and after-checks, and gates ReviewPaths.
// Merge "after-checks" (with after-review or automatic) then waits for the pull request's checks and squash-merges it when
// all of them pass; with failing, missing or unfinished checks, or a conflict, it leaves the pull request open.
type PullRequest struct {
	Mode        string   `json:"mode"`
	Base        string   `json:"base,omitempty"`  // target branch; default: the branch of baseRef, else the default branch
	Merge       string   `json:"merge,omitempty"` // "" (a person merges) or "after-checks"
	ReviewPaths []string `json:"reviewPaths,omitempty"`
}

func (i Intake) label() string        { return orDefault(i.Label, "agent-ready") }
func (i Intake) workingLabel() string { return orDefault(i.WorkingLabel, "agent-working") }

// IssueRef is the work item a Run took.
type IssueRef struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Author string `json:"author"`
}

// EnvVar is injected into agent and command steps. Secret values live in the system keychain;
// the definition stores only a keyring:/env: reference.
type EnvVar struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"`
}

// NotifyRule selects channels and events for this Loop. Empty Channels = all enabled channels;
// empty Events = done, attention and failed (no_action stays silent).
type NotifyRule struct {
	Channels      []string `json:"channels,omitempty"`
	Events        []string `json:"events,omitempty"`
	IncludeReport bool     `json:"includeReport,omitempty"`
}

// ChatMessage is one turn of the authoring conversation.
type ChatMessage struct {
	Role string    `json:"role"` // user or agent
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// StepRecord is one executed step of one round; the UI builds the round timeline from it.
type StepRecord struct {
	Attempt  int        `json:"attempt,omitempty"`
	Agent    string     `json:"agent,omitempty"`
	Round    int        `json:"round"`
	Step     int        `json:"step"`
	Kind     string     `json:"kind"`
	Name     string     `json:"name"`
	Status   string     `json:"status"` // running, waiting, ok, failed, approved, rejected, cancelled
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
	Artifact string     `json:"artifact,omitempty"`
	Bytes    int        `json:"bytes,omitempty"`
	Note     string     `json:"note,omitempty"`
	Model    string     `json:"model,omitempty"`  // requested model (agent steps only)
	Effort   string     `json:"effort,omitempty"` // requested effort (agent steps only)
	Usage    *Usage     `json:"usage,omitempty"`  // model and tokens of an agent step, as the CLI reported them
}
type Run struct {
	Metrics    map[string]float64 `json:"metrics,omitempty"`
	ID         string             `json:"id"`
	LoopID     string             `json:"loopId"`
	Definition Loop               `json:"definition"`
	Status     string             `json:"status"`
	Round      int                `json:"round"`
	Step       int                `json:"step"`
	Started    time.Time          `json:"started"`
	Finished   *time.Time         `json:"finished,omitempty"`
	Error      string             `json:"error,omitempty"`
	Artifacts  []string           `json:"artifacts"`
	Trace      []StepRecord       `json:"trace,omitempty"`
	Source     string             `json:"source,omitempty"` // manual, schedule, catchup, rerun
	RerunOf    string             `json:"rerunOf,omitempty"`
	Acked      bool               `json:"acked,omitempty"`
	Report     string             `json:"report,omitempty"` // the agent's last final message (truncated)
	Result     *AgentResult       `json:"result,omitempty"` // parsed result contract of the last agent step
	Workdir    string             `json:"workdir,omitempty"`
	Branch     string             `json:"branch,omitempty"`  // branch loopbee created for this Run
	Commits    []string           `json:"commits,omitempty"` // commits loopbee made on it (short hashes)
	Issue      *IssueRef          `json:"issue,omitempty"`   // the work item taken by the Loop's intake
	PRURL      string             `json:"prUrl,omitempty"`   // pull request opened by the Loop's delivery
	Merged     bool               `json:"merged,omitempty"`  // the pull request was merged by the Loop's delivery
	Phase      string             `json:"phase,omitempty"`   // what a running Run does between steps: "delivering", "merging"
	Usage      *Usage             `json:"usage,omitempty"`   // models and tokens of all agent steps of this Run
}
type Event struct {
	ID    int64     `json:"id"`
	RunID string    `json:"runId"`
	Time  time.Time `json:"time"`
	Type  string    `json:"type"`
	Text  string    `json:"text"`
}
type Channel struct {
	Kind     string `json:"kind"`
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	Secret   string `json:"secret,omitempty"`
	Target   string `json:"target"`
}
type Settings struct {
	DigestEnabled  bool      `json:"digestEnabled"`
	DigestCron     string    `json:"digestCron"`
	DigestTimezone string    `json:"digestTimezone"`
	MaxSandbox     string    `json:"maxSandbox"`
	KeepAwake      bool      `json:"keepAwake"`
	StartAtLogin   bool      `json:"startAtLogin"`
	Language       string    `json:"language"`
	ReportLanguage string    `json:"reportLanguage"`
	DigestLanguage string    `json:"digestLanguage"`
	Theme          string    `json:"theme"`
	Motion         bool      `json:"motion"`
	Role           string    `json:"role"`
	RetentionDays  int       `json:"retentionDays"`
	Channels       []Channel `json:"channels"`
	TeamRemote     string    `json:"teamRemote"`
	TeamBranch     string    `json:"teamBranch"`
	Publisher      string    `json:"publisher"`
	Onboarded      bool      `json:"onboarded"`
	DefaultAgent   string    `json:"defaultAgent"`
	Paused         bool      `json:"paused"`
	// NoAutoUpdate turns off the automatic update checks and downloads (Settings › General); “Check now” still works.
	NoAutoUpdate bool `json:"noAutoUpdate"`
}
type Delivery struct {
	ID       string    `json:"id"`
	Channel  string    `json:"channel"`
	Text     string    `json:"text"`
	Status   string    `json:"status"`
	Attempts int       `json:"attempts"`
	Error    string    `json:"error"`
	Updated  time.Time `json:"updated"`
}
type State struct {
	Evidence   map[string]Evidence `json:"evidence,omitempty"`
	Activity   []Event             `json:"activity"`
	Brand      string              `json:"brand"`
	Loops      []Loop              `json:"loops"`
	Runs       []Run               `json:"runs"`
	Settings   Settings            `json:"settings"`
	Deliveries []Delivery          `json:"deliveries"`
	Agents     []Agent             `json:"agents"`
	GitHub     GitHubStatus        `json:"github"` // GitHub CLI for issue intake and pull requests
	Update     UpdateStatus        `json:"update"` // loopbee's own version and updates
	Team       TeamStatus          `json:"team"`   // the last team sync, automatic or not
	Updated    time.Time           `json:"updated"`
	Info       Info                `json:"info"`
}

// Info describes this installation for the About page and diagnostics.
type Info struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	DataDir string `json:"dataDir"`
	Active  int    `json:"active"`
	CanPick bool   `json:"canPick"` // the desktop app can open the system's folder chooser
}

type Evidence struct {
	Events    []Event           `json:"events"`
	Artifacts map[string]string `json:"artifacts"`
}

func schedule(l Loop) (cron.Schedule, error) {
	if _, err := time.LoadLocation(l.Timezone); err != nil {
		return nil, fmt.Errorf("invalid timezone: %w", err)
	}
	return cron.ParseStandard("CRON_TZ=" + l.Timezone + " " + l.Cron)
}

// isAgentStep is the single classification shared by validation and execution.
func isAgentStep(kind string) bool {
	switch kind {
	case "observe", "act", "summarize", "review":
		return true
	}
	return false
}

var stepIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

var branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/{}-]{0,120}$`)

func Validate(l Loop) error {
	if _, err := condition(l.DoneWhen, nil, false); err != nil {
		return err
	}
	if strings.TrimSpace(l.Name) == "" || len(l.Name) > 160 {
		return errors.New("name is required (maximum 160 characters)")
	}
	if err := validProject(l.Project); err != nil {
		return err
	}
	if !filepath.IsAbs(l.Directory) {
		return errors.New("working directory must be absolute")
	}
	info, err := os.Stat(l.Directory)
	if err != nil || !info.IsDir() {
		return errors.New("working directory does not exist")
	}
	if _, ok := adapterFor(l.Agent); !ok {
		return levelError(l.Agent, l.Sandbox)
	}
	if !validSandbox(l.Sandbox) {
		return errors.New("permission must be read-only, workspace-write, workspace-network or agent-config")
	}
	if !supportsLevel(l.Agent, l.Sandbox) {
		return levelError(l.Agent, l.Sandbox)
	}
	if l.Model != "" && (len(l.Model) > 120 || strings.HasPrefix(l.Model, "-") || strings.ContainsAny(l.Model, " \t\r\n")) {
		return errors.New("model must be a model name without spaces, e.g. gpt-5.5 or claude-sonnet-4-5")
	}
	if l.Effort != "" {
		if a, ok := adapterFor(l.Agent); ok && len(a.Efforts) > 0 && !slices.Contains(a.Efforts, l.Effort) {
			return fmt.Errorf("%s does not support the %q effort level; choose %s", agentLabel(l.Agent), l.Effort, strings.Join(a.Efforts, ", "))
		}
	}
	if l.Workspace != "" && l.Workspace != "direct" && l.Workspace != "worktree" {
		return errors.New("workspace must be direct or worktree")
	}
	if l.BaseRef != "" && (strings.HasPrefix(l.BaseRef, "-") || strings.ContainsAny(l.BaseRef, " \t\n;&|$`")) {
		return errors.New("invalid base ref")
	}
	if l.Branch != "" {
		if l.Workspace != "worktree" {
			return errors.New("a branch needs the fresh git worktree workspace")
		}
		if !branchPattern.MatchString(l.Branch) || strings.Contains(l.Branch, "..") || strings.HasSuffix(l.Branch, "/") || strings.HasSuffix(l.Branch, ".lock") || strings.Contains(l.Branch, "//") {
			return errors.New("invalid branch name")
		}
		switch strings.ToLower(l.Branch) {
		case "master", "main", "head", "develop", "production", "release":
			return errors.New("loopbee never commits to a shared branch; use a name such as loopbee/fix-{date}")
		}
	}
	home, _ := os.UserHomeDir()
	if len(l.ReadPaths) > 20 {
		return errors.New("a Loop can list at most 20 read paths")
	}
	for _, p := range l.ReadPaths {
		if err := validateReadPath(p, home); err != nil {
			return err
		}
	}
	if len(l.CopyFiles) > 0 && l.Workspace != "worktree" {
		return errors.New("copied files are for the fresh git worktree workspace; in the project folder they are already there")
	}
	if len(l.CopyFiles) > 50 {
		return errors.New("a Loop can copy at most 50 files or folders")
	}
	for _, p := range l.CopyFiles {
		if err := validateCopyFile(p); err != nil {
			return err
		}
	}
	if err := validateIntakeAndDelivery(l); err != nil {
		return err
	}
	if l.GateHours < 0 || l.GateHours > 24*14 {
		return errors.New("gate expiry must be 1–336 hours")
	}
	seenEnv := map[string]bool{}
	for _, e := range l.Env {
		if !envName.MatchString(e.Name) || seenEnv[e.Name] {
			return errors.New("environment variable names must be unique UPPER_SNAKE_CASE")
		}
		seenEnv[e.Name] = true
		if e.Secret && e.Value != "" && !strings.HasPrefix(e.Value, "keyring:") && !strings.HasPrefix(e.Value, "env:") {
			return errors.New("secret values must be stored in the keychain")
		}
	}
	for _, c := range l.Notify.Channels {
		if c != "desktop" && c != "feishu" && c != "telegram" && c != "bark" {
			return errors.New("unknown notification channel")
		}
	}
	for _, e := range l.Notify.Events {
		if e != "done" && e != "attention" && e != "failed" && e != "no_action" {
			return errors.New("unknown notification event")
		}
	}
	if l.MaxRounds < 1 || l.MaxRounds > 20 || l.WallMinutes < 1 || l.WallMinutes > 1440 || l.StallRounds < 1 || l.StallRounds > 20 {
		return errors.New("limits require rounds 1–20, wall minutes 1–1440 and stall rounds 1–20")
	}
	if l.Cron != "" {
		if _, err := schedule(l); err != nil {
			return err
		}
	}
	if len(l.Steps) == 0 || len(l.Steps) > 50 {
		return errors.New("flow requires 1–50 steps")
	}
	seenIDs := map[string]bool{}
	for _, s := range l.Steps {
		// Missing IDs are allowed on unsaved drafts; SaveLoop assigns them.
		if s.ID != "" {
			if !stepIDPattern.MatchString(s.ID) || seenIDs[s.ID] {
				return fmt.Errorf("step %q: id must be unique within the Loop; use 1–40 lowercase letters, digits or hyphens", s.Name)
			}
			seenIDs[s.ID] = true
		}
	}
	earlier := map[string]Step{}
	deliveries := 0
	for i, s := range l.Steps {
		if !isAgentStep(s.Kind) && (s.Agent != "" || s.Sandbox != "" || len(s.Inputs) > 0 || s.Model != "" || s.Effort != "" || s.Prompt != "") {
			return fmt.Errorf("step %q: agent, sandbox, inputs, model, effort and prompt apply only to agent or review steps; remove them or change the kind", s.Name)
		}
		if s.Kind != "review" && (s.SendBackTo != "" || s.MaxAttempts != 0) {
			return fmt.Errorf("step %q: sendBackTo and maxAttempts apply only to review steps; remove them or use review", s.Name)
		}
		if s.Kind != "deliver" && s.PullRequest != nil {
			return fmt.Errorf("step %q: pullRequest applies only to deliver steps; remove it or use deliver", s.Name)
		}
		if s.Kind != "run" && s.Kind != "verify" && len(s.Argv) > 0 {
			return fmt.Errorf("step %q: argv applies only to run or verify steps; remove it or change the kind", s.Name)
		}
		if s.Kind != "verify" && (s.Metric != "" || s.MetricPath != "") {
			return fmt.Errorf("step %q: metric and metricPath apply only to verify steps; remove them or use verify", s.Name)
		}
		for _, id := range s.Inputs {
			if prior, ok := earlier[id]; !ok || !isAgentStep(prior.Kind) {
				return fmt.Errorf("step %q: inputs %q must name an earlier agent or review step; choose its step ID", s.Name, id)
			}
		}
		if s.Kind == "review" {
			prior, ok := earlier[s.SendBackTo]
			if !ok || !isAgentStep(prior.Kind) || prior.Kind == "review" {
				return fmt.Errorf("step %q: sendBackTo is required; choose the ID of an earlier observe, act or summarize step", s.Name)
			}
			if s.MaxAttempts < 0 || s.MaxAttempts > 10 {
				return fmt.Errorf("step %q: maxAttempts must be 1–10, or 0 to use the default 3", s.Name)
			}
		}
		if s.Kind == "deliver" {
			deliveries++
			if deliveries > 1 || l.PullRequest != nil {
				return errors.New("use at most one deliver step and remove Loop.pullRequest when using deliver")
			}
			if s.PullRequest == nil {
				return fmt.Errorf("step %q: deliver requires pullRequest; set its mode to draft, after-review or automatic", s.Name)
			}
			delivery := l
			delivery.PullRequest = s.PullRequest
			delivery.Steps = l.Steps[:i]
			if err := validatePullRequest(delivery); err != nil {
				return err
			}
		}
		if s.ID != "" {
			earlier[s.ID] = s
		}
		if strings.TrimSpace(s.Name) == "" {
			return errors.New("step name required")
		}
		switch {
		case s.Kind == "gate" || s.Kind == "deliver":
		case isAgentStep(s.Kind):
			if strings.TrimSpace(s.Prompt) == "" {
				return errors.New("agent step requires a prompt")
			}
			agent := orDefault(s.Agent, l.Agent)
			if !supportsLevel(agent, orDefault(s.Sandbox, l.Sandbox)) {
				return fmt.Errorf("step %q: %w", s.Name, levelError(agent, orDefault(s.Sandbox, l.Sandbox)))
			}
			// Validate step-level model and effort overrides.
			if s.Model != "" && (len(s.Model) > 120 || strings.HasPrefix(s.Model, "-") || strings.ContainsAny(s.Model, " \t\r\n")) {
				return errors.New("model must be a model name without spaces, e.g. gpt-5.5 or claude-sonnet-4-5")
			}
			if s.Effort != "" {
				if a, ok := adapterFor(agent); ok && len(a.Efforts) > 0 && !slices.Contains(a.Efforts, s.Effort) {
					return fmt.Errorf("step %q: %s does not support the %q effort level; choose %s", s.Name, agentLabel(agent), s.Effort, strings.Join(a.Efforts, ", "))
				}
			}
		case s.Kind == "run":
			if len(s.Argv) == 0 || !slices.Contains(l.AllowedTools, s.Argv[0]) {
				return errors.New("command executable must be explicitly allowed")
			}
		case s.Kind == "verify":
			if (s.Metric == "") != (s.MetricPath == "") {
				return errors.New("metric name and path must be provided together")
			}
			if len(s.Argv) == 0 || !slices.Contains(l.AllowedTools, s.Argv[0]) {
				return errors.New("verification executable must be explicitly allowed")
			}
		default:
			return fmt.Errorf("unknown step kind: %s", s.Kind)
		}
		if s.ContinueOnError && s.Kind != "run" {
			return errors.New("only command steps can continue on error")
		}
	}
	return nil
}

var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// reviewPathMatch matches repository-relative paths; * and ? stay within one segment, ** crosses segments.
func reviewPathMatch(pattern, name string) bool {
	var expression strings.Builder
	expression.WriteString("(?s)^")
	segments := strings.Split(pattern, "/")
	for i, segment := range segments {
		if segment == "**" {
			if i == len(segments)-1 {
				expression.WriteString(".*")
			} else {
				expression.WriteString("(?:[^/]+/)*")
			}
			continue
		}
		for _, c := range segment {
			switch c {
			case '*':
				expression.WriteString("[^/]*")
			case '?':
				expression.WriteString("[^/]")
			default:
				expression.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
		if i == len(segments)-2 && segments[i+1] == "**" {
			expression.WriteString("(?:/.*)?")
			break
		}
		if i < len(segments)-1 {
			expression.WriteByte('/')
		}
	}
	expression.WriteByte('$')
	matched, _ := regexp.MatchString(expression.String(), name)
	return matched
}

func terminal(s string) bool { return s != "running" && s != "waiting" && s != "queued" }
func language(s string) bool { return s == "en" || s == "zh-TW" || s == "zh-CN" }

var (
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
	loginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	labelPattern = regexp.MustCompile(`^[^\x00-\x1f,]{1,50}$`)
)

func validateIntakeAndDelivery(l Loop) error {
	if l.PullRequest != nil && l.PullRequest.Mode == "automatic" {
		return errors.New("automatic delivery is only allowed on a deliver step; remove Loop.pullRequest")
	}
	if strings.Contains(l.Branch, "{issue}") && l.Intake == nil {
		return errors.New("{issue} in the branch name needs GitHub issues as the work source")
	}
	needsBranch := func(what string) error {
		if l.Workspace != "worktree" || l.Branch == "" {
			return errors.New(what + " needs a fresh git worktree and a branch")
		}
		return nil
	}
	if in := l.Intake; in != nil {
		if in.Kind != "github-issues" {
			return errors.New("work source must be github-issues")
		}
		if err := needsBranch("Working from issues"); err != nil {
			return err
		}
		if in.Repo != "" && !repoPattern.MatchString(in.Repo) {
			return errors.New("issue repository must look like owner/name")
		}
		for _, lb := range []string{in.label(), in.workingLabel()} {
			if !labelPattern.MatchString(lb) {
				return errors.New("invalid issue label")
			}
		}
		if in.label() == in.workingLabel() {
			return errors.New("the ready and working labels must differ")
		}
		for _, u := range in.TrustedUsers {
			if !loginPattern.MatchString(u) {
				return fmt.Errorf("invalid GitHub login %q", u)
			}
		}
		if !slices.ContainsFunc(l.Steps, func(s Step) bool { return isAgentStep(s.Kind) }) {
			return errors.New("working from issues needs an agent step")
		}
	}
	return validatePullRequest(l)
}

func validatePullRequest(l Loop) error {
	if pr := l.PullRequest; pr != nil {
		if l.Workspace != "worktree" || l.Branch == "" {
			return errors.New("A pull request needs a fresh git worktree and a branch; set workspace to worktree and provide branch")
		}
		if pr.Base != "" && (strings.HasPrefix(pr.Base, "-") || strings.ContainsAny(pr.Base, " \t\n;&|$`~^:?*[\\")) {
			return errors.New("invalid pull request base branch; use a branch name without spaces or revision/shell metacharacters")
		}
		for _, pattern := range pr.ReviewPaths {
			if strings.TrimSpace(pattern) == "" || strings.HasPrefix(pattern, "/") || strings.ContainsAny(pattern, "\\:\x00\r\n") || slices.Contains(strings.Split(pattern, "/"), "..") {
				return fmt.Errorf("invalid review path %q; use a non-empty repository-relative pattern without ..", pattern)
			}
		}
		if pr.Mode != "automatic" && len(pr.ReviewPaths) > 0 {
			return errors.New("reviewPaths apply only to automatic delivery; choose automatic or remove them")
		}
		switch pr.Mode {
		case "draft":
		case "automatic":
			if pr.Merge != "after-checks" || strings.TrimSpace(pr.Base) == "" {
				return errors.New("automatic delivery needs merge after-checks and a non-empty test base; set both before saving")
			}
		case "after-review":
			lastAgent, lastGate := -1, -1
			for i, s := range l.Steps {
				if s.Kind == "gate" {
					lastGate = i
				}
				if isAgentStep(s.Kind) {
					lastAgent = i
				}
			}
			if lastGate < lastAgent || lastGate < 0 {
				return errors.New("a pull request after review needs a gate after the last agent step")
			}
		default:
			return errors.New("pull request mode must be after-review, automatic or draft")
		}
		switch pr.Merge {
		case "":
		case "after-checks":
			if pr.Mode != "after-review" && pr.Mode != "automatic" {
				return errors.New("merging automatically needs “after my review” or “automatic when checks pass”: a draft has not been reviewed")
			}
		default:
			return errors.New("pull request merge must be empty or after-checks")
		}
	}
	return nil
}
