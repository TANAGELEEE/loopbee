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

const Brand = "Loopbee"

// Version is overridden at build time with -ldflags "-X loopbee/internal/core.Version=…".
var Version = "0.1.0-dev"

type Step struct {
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
}
type Loop struct {
	DoneWhen     string    `json:"doneWhen"`
	ID           string    `json:"id"`
	Version      int       `json:"version"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Directory    string    `json:"directory"`
	Agent        string    `json:"agent"`
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
	// Branch (worktree only): Loopbee creates this branch for the Run and commits the agent's changes on it
	// after each agent step, so agents never write Git metadata (Codex's sandbox forbids it).
	// {date}, {time} and {run} are replaced per Run.
	Branch string `json:"branch,omitempty"`
	// GateHours is how long a gate may wait for a decision; waiting does not consume WallMinutes.
	GateHours    int           `json:"gateHours,omitempty"`
	Env          []EnvVar      `json:"env,omitempty"`
	Notify       NotifyRule    `json:"notify,omitempty"`
	Conversation []ChatMessage `json:"conversation,omitempty"` // authoring chat that produced this definition
	Source       string        `json:"source,omitempty"`       // where the definition came from (URL, template, chat)
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
	Branch     string             `json:"branch,omitempty"`  // branch Loopbee created for this Run
	Commits    []string           `json:"commits,omitempty"` // commits Loopbee made on it (short hashes)
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

var branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/{}-]{0,120}$`)

func Validate(l Loop) error {
	if _, err := condition(l.DoneWhen, nil, false); err != nil {
		return err
	}
	if strings.TrimSpace(l.Name) == "" || len(l.Name) > 160 {
		return errors.New("name is required (maximum 160 characters)")
	}
	if !filepath.IsAbs(l.Directory) {
		return errors.New("working directory must be absolute")
	}
	info, err := os.Stat(l.Directory)
	if err != nil || !info.IsDir() {
		return errors.New("working directory does not exist")
	}
	if l.Agent != "codex" && l.Agent != "claude" {
		return errors.New("unknown agent")
	}
	if !validSandbox(l.Sandbox) {
		return errors.New("permission must be read-only, workspace-write, workspace-network or agent-config")
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
			return errors.New("Loopbee never commits to a shared branch; use a name such as loopbee/fix-{date}")
		}
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
	for _, s := range l.Steps {
		if strings.TrimSpace(s.Name) == "" {
			return errors.New("step name required")
		}
		switch s.Kind {
		case "gate":
		case "observe", "act", "summarize":
			if strings.TrimSpace(s.Prompt) == "" {
				return errors.New("agent step requires a prompt")
			}
		case "run":
			if len(s.Argv) == 0 || !slices.Contains(l.AllowedTools, s.Argv[0]) {
				return errors.New("command executable must be explicitly allowed")
			}
		case "verify":
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

func terminal(s string) bool { return s != "running" && s != "waiting" && s != "queued" }
func language(s string) bool { return s == "en" || s == "zh-TW" || s == "zh-CN" }
