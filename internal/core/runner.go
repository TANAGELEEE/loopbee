package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type boundedOutput struct {
	mu      sync.Mutex
	b       bytes.Buffer
	limit   int
	onLine  func(string)
	pending string
	err     error
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.b.Len()+len(p) > w.limit {
		w.err = errors.New("process output exceeds 8 MiB limit")
		return 0, w.err
	}
	w.b.Write(p)
	if w.onLine != nil {
		w.pending += string(p)
		for {
			idx := strings.IndexByte(w.pending, '\n')
			if idx < 0 {
				break
			}
			w.onLine(w.pending[:idx])
			w.pending = w.pending[idx+1:]
		}
	}
	return len(p), nil
}
func runCommand(ctx context.Context, dir string, argv []string, onLine func(string)) ([]byte, error) {
	return runCommandInput(ctx, dir, argv, onLine, nil)
}
func runCommandInput(ctx context.Context, dir string, argv []string, onLine func(string), input io.Reader) ([]byte, error) {
	return runCommandEnv(ctx, dir, argv, onLine, input, nil)
}

// runCommandEnv runs argv without a shell. extraEnv entries (NAME=value) are added to the inherited environment.
func runCommandEnv(ctx context.Context, dir string, argv []string, onLine func(string), input io.Reader, extraEnv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = input
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	configureProcess(cmd)
	output := &boundedOutput{limit: 8 << 20, onLine: onLine}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if output.pending != "" && onLine != nil {
		onLine(output.pending)
	}
	if output.err != nil {
		return output.b.Bytes(), output.err
	}
	if ctx.Err() != nil {
		return output.b.Bytes(), ctx.Err()
	}
	return output.b.Bytes(), err
}

// Start begins a manual Run of the Loop.
func (a *App) Start(id string) (Run, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.startLocked(id, "manual", "")
}

// Rerun starts a fresh Run of the Loop that produced the given Run. The old Run is untouched.
func (a *App) Rerun(runID string) (Run, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	old, err := a.Run(runID)
	if err != nil {
		return Run{}, err
	}
	if !terminal(old.Status) {
		return Run{}, errors.New("run is still active")
	}
	return a.startLocked(old.LoopID, "rerun", old.ID)
}

// Acknowledge records that an operator reviewed an interrupted Run. It never restarts anything.
func (a *App) Acknowledge(runID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role != "executor" {
		return errors.New("viewer is read-only")
	}
	r, err := a.Run(runID)
	if err != nil {
		return err
	}
	if r.Status != "interrupted" {
		return errors.New("only interrupted runs need acknowledgement")
	}
	r.Acked = true
	if err = a.saveRun(r); err != nil {
		return err
	}
	return a.event(r.ID, "run.acknowledged", "")
}
func (a *App) startLocked(id, source, rerunOf string) (Run, error) {
	var l Loop
	var r Run
	if a.ctx.Err() != nil {
		return r, errors.New("application shutting down")
	}
	if a.config.Role != "executor" {
		return r, errors.New("viewer is read-only")
	}
	if err := a.get("loop", id, &l); err != nil {
		return r, err
	}
	if err := Validate(l); err != nil {
		return r, err
	}
	if !sandboxWithin(l.Sandbox, a.config.MaxSandbox) {
		return r, errors.New("permission exceeds this device's limit")
	}
	runs, err := a.Runs()
	if err != nil {
		return r, err
	}
	for _, old := range runs {
		if !terminal(old.Status) && (old.LoopID == id || (l.Lock != "" && old.Definition.Lock == l.Lock)) {
			return r, errors.New("loop or resource lock is already active")
		}
	}
	r = Run{ID: newID(), LoopID: id, Definition: l, Status: "queued", Started: time.Now(), Round: 1, Artifacts: []string{}, Source: source, RerunOf: rerunOf}
	if err = a.saveRun(r); err != nil {
		return r, err
	}
	// The wall-clock budget is enforced per step inside execute so that waiting at a gate does not consume it.
	ctx, cancel := context.WithCancel(a.ctx)
	a.active[r.ID] = cancel
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer cancel()
		a.execute(ctx, r)
		a.mu.Lock()
		delete(a.active, r.ID)
		delete(a.gates, r.ID)
		a.mu.Unlock()
	}()
	return r, nil
}
func (a *App) execute(ctx context.Context, r Run) {
	l := r.Definition
	// mark closes the most recent trace record of the current step.
	mark := func(status, note string) {
		if n := len(r.Trace); n > 0 && r.Trace[n-1].Finished == nil {
			now := time.Now()
			r.Trace[n-1].Status = status
			r.Trace[n-1].Finished = &now
			if note != "" {
				r.Trace[n-1].Note = note
			}
		}
	}
	finish := func(status string, err error) {
		switch status {
		case "cancelled", "timed_out":
			mark("cancelled", "")
		case "rejected":
		default:
			if err != nil {
				mark("failed", err.Error())
			} else {
				mark("ok", "")
			}
		}
		r.Status = status
		if err != nil {
			r.Error = err.Error()
		}
		now := time.Now()
		r.Finished = &now
		if e := a.saveRun(r); e != nil {
			a.event(r.ID, "storage.error", e.Error())
			return
		}
		a.event(r.ID, "run."+status, r.Error)
		a.notifyRun(r)
	}
	// Wall-clock budget: only time spent executing steps counts; gates have their own expiry.
	budget := time.Duration(l.WallMinutes) * wallUnit
	var used time.Duration
	dir := filepath.Join(a.Dir, "runs", r.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		finish("failed", err)
		return
	}
	prepCtx, prepCancel := context.WithTimeout(ctx, 5*time.Minute)
	workdir, extraDirs, branch, err := a.prepareWorkspace(prepCtx, l, r.ID)
	prepCancel()
	if err != nil {
		if ctx.Err() != nil {
			finish(cancelStatus(ctx.Err()), ctx.Err())
			return
		}
		finish("blocked", err)
		return
	}
	r.Workdir = workdir
	r.Branch = branch
	env, err := loopEnv(l)
	if err != nil {
		finish("blocked", err)
		return
	}
	if l.RequireClean && l.Workspace != "worktree" {
		cctx, ccancel := context.WithTimeout(ctx, time.Minute)
		out, err := runCommand(cctx, workdir, []string{"git", "status", "--porcelain"}, nil)
		ccancel()
		if err != nil || len(bytes.TrimSpace(out)) > 0 {
			finish("blocked", errors.New("working tree must be a clean Git repository"))
			return
		}
	}
	r.Status = "running"
	if err := a.saveRun(r); err != nil {
		finish("failed", err)
		return
	}
	gateWait := time.Duration(l.GateHours) * gateUnit
	if gateWait <= 0 {
		gateWait = 72 * gateUnit
	}
	reviewerNote := ""
	var lastHash [32]byte
	stall := 0
	for round := 1; round <= l.MaxRounds; round++ {
		r.Round = round
		verified := true
		r.Metrics = map[string]float64{}
		var progress bytes.Buffer
		var earlier strings.Builder // output of command steps, handed to later agent steps of this round
		for i, s := range l.Steps {
			if err := ctx.Err(); err != nil {
				finish(cancelStatus(err), err)
				return
			}
			if used >= budget {
				finish("timed_out", fmt.Errorf("time limit of %d minutes reached", l.WallMinutes))
				return
			}
			r.Step = i
			r.Trace = append(r.Trace, StepRecord{Round: round, Step: i, Kind: s.Kind, Name: s.Name, Status: "running", Started: time.Now()})
			if err := a.saveRun(r); err != nil {
				finish("failed", err)
				return
			}
			if err := a.event(r.ID, "step.started", s.Name); err != nil {
				finish("failed", err)
				return
			}
			if s.Kind == "gate" {
				gate := make(chan gateDecision, 1)
				a.mu.Lock()
				a.gates[r.ID] = gate
				r.Status = "waiting"
				r.Trace[len(r.Trace)-1].Status = "waiting"
				err := a.saveRun(r)
				a.mu.Unlock()
				if err != nil {
					finish("failed", err)
					return
				}
				a.notifyRun(r)
				timer := time.NewTimer(gateWait)
				select {
				case decision := <-gate:
					timer.Stop()
					if !decision.approve {
						mark("rejected", decision.note)
						finish("rejected", nil)
						return
					}
					mark("approved", decision.note)
					reviewerNote = decision.note
				case <-timer.C:
					a.mu.Lock()
					delete(a.gates, r.ID)
					a.mu.Unlock()
					mark("expired", "")
					finish("timed_out", fmt.Errorf("gate expired after %d hours without a decision", int(gateWait/gateUnit)))
					return
				case <-ctx.Done():
					timer.Stop()
					finish(cancelStatus(ctx.Err()), ctx.Err())
					return
				}
				r.Status = "running"
				continue
			}
			var argv []string
			var input io.Reader
			agentStep := s.Kind != "verify" && s.Kind != "run"
			lastFile := ""
			if !agentStep {
				argv = s.Argv
			} else {
				ag, ok := a.agentInfo(l.Agent)
				if !ok || ag.Status != "available" {
					finish("blocked", errors.New("agent version unsupported; run detection first"))
					return
				}
				base, err := agentCommand(l.Agent)
				if err != nil {
					finish("blocked", err)
					return
				}
				if l.Agent == "codex" && hasCap(ag.Caps, "last-message") {
					lastFile = filepath.Join(dir, fmt.Sprintf("round-%02d-step-%02d.report.md", round, i+1))
				}
				argv = buildAgentArgs(base, ag.Caps, agentInvocation{Name: l.Agent, Dir: workdir, Sandbox: l.Sandbox, AddDirs: extraDirs, LastFile: lastFile})
				prompt := s.Prompt
				if r.Branch != "" {
					prompt += "\n\nYou are on branch " + r.Branch + " in a fresh worktree that Loopbee created from " + orDefault(l.BaseRef, "HEAD") + ". Do not create or switch branches and do not commit: Loopbee commits your changes on this branch after this step."
				}
				if earlier.Len() > 0 {
					prompt += "\n\nOutput of the earlier steps of this run (captured by Loopbee; use it instead of re-running the commands):\n" + earlier.String()
				}
				if reviewerNote != "" {
					prompt += "\n\nNote from the human reviewer at the previous gate: " + reviewerNote
				}
				prompt += resultInstructions(a.Settings().ReportLanguage)
				input = strings.NewReader(prompt)
			}
			stepCtx, stepCancel := context.WithTimeout(ctx, budget-used)
			started := time.Now()
			var eventErr error
			agentFailed := false
			out, err := runCommandEnv(stepCtx, workdir, argv, func(line string) {
				kind := "command.output"
				if agentStep {
					var failed bool
					kind, failed = classifyAgentEvent(line)
					agentFailed = agentFailed || failed
				}
				if e := a.event(r.ID, kind, line); e != nil {
					eventErr = e
				}
			}, input, env)
			stepErr := stepCtx.Err()
			stepCancel()
			used += time.Since(started)
			name := fmt.Sprintf("round-%02d-step-%02d.txt", round, i+1)
			if e := os.WriteFile(filepath.Join(dir, name), out, 0600); e != nil {
				finish("failed", e)
				return
			}
			r.Artifacts = append(r.Artifacts, name)
			r.Trace[len(r.Trace)-1].Artifact = name
			r.Trace[len(r.Trace)-1].Bytes = len(out)
			progress.Write(out)
			if eventErr != nil {
				finish("failed", eventErr)
				return
			}
			if ctx.Err() != nil {
				finish(cancelStatus(ctx.Err()), ctx.Err())
				return
			}
			if errors.Is(stepErr, context.DeadlineExceeded) {
				finish("timed_out", fmt.Errorf("time limit of %d minutes reached during “%s”", l.WallMinutes, s.Name))
				return
			}
			if agentStep {
				res := a.captureReport(&r, dir, lastFile, round, i, out)
				if agentFailed {
					if reason := agentFailure(out); reason != "" {
						finish("failed", errors.New("agent failed: "+reason))
					} else {
						finish("failed", errors.New("agent reported turn.failed; inspect the captured evidence"))
					}
					return
				}
				if err != nil {
					finish("failed", err)
					return
				}
				if res != nil {
					switch res.Status {
					case "no_action":
						mark("ok", res.Summary)
						finish("no_action", nil)
						return
					case "blocked":
						msg := res.Summary
						if msg == "" {
							msg = "agent reported that it is blocked"
						}
						finish("blocked", errors.New(msg))
						return
					}
				}
				note := ""
				if res != nil {
					note = res.Summary
				}
				if r.Branch != "" {
					subject := l.Name + ": " + s.Name
					if res != nil && res.Summary != "" {
						subject = l.Name + ": " + firstLine(res.Summary, 120)
					}
					cctx, ccancel := context.WithTimeout(ctx, 2*time.Minute)
					sha, err := commitWorkspace(cctx, workdir, subject+"\n\nCommitted by Loopbee for run "+r.ID+".")
					ccancel()
					if err != nil {
						mark("failed", err.Error())
						finish("failed", fmt.Errorf("could not commit the agent's changes on %s: %w", r.Branch, err))
						return
					}
					if sha != "" {
						r.Commits = append(r.Commits, sha)
						note = strings.TrimSpace(note + " · " + r.Branch + " @ " + sha)
						a.event(r.ID, "workspace.commit", r.Branch+" @ "+sha)
					}
				}
				mark("ok", note)
				a.event(r.ID, "step.completed", s.Name)
				continue
			}
			exit := "succeeded"
			if err != nil {
				exit = "failed: " + err.Error()
			}
			fmt.Fprintf(&earlier, "\n### %s — `%s` (%s)\n```\n%s\n```\n", s.Name, strings.Join(argv, " "), exit, tail(string(out), 6000))
			if err != nil && s.ContinueOnError {
				a.event(r.ID, "step.failed", err.Error())
				mark("failed", err.Error()+" · continuing")
				a.event(r.ID, "step.completed", s.Name)
				continue
			}
			if err != nil {
				a.event(r.ID, "step.failed", err.Error())
				if s.Kind == "verify" {
					verified = false
					mark("failed", err.Error())
					break
				}
				finish("failed", fmt.Errorf("command “%s” failed: %w", s.Name, err))
				return
			}
			if s.Kind == "verify" && s.Metric != "" {
				value, e := metricValue(out, s.MetricPath)
				if e != nil {
					finish("failed", e)
					return
				}
				r.Metrics[s.Metric] = value
				r.Trace[len(r.Trace)-1].Note = fmt.Sprintf("%s = %g", s.Metric, value)
			}
			mark("ok", "")
			a.event(r.ID, "step.completed", s.Name)
		}
		done, err := condition(l.DoneWhen, r.Metrics, verified)
		if err != nil {
			finish("failed", err)
			return
		}
		if verified && done {
			finish("completed", nil)
			return
		}
		progressData := progress.Bytes()
		if len(r.Metrics) > 0 {
			progressData, _ = json.Marshal(r.Metrics)
		}
		hash := sha256.Sum256(progressData)
		if hash == lastHash {
			stall++
		} else {
			stall = 0
		}
		lastHash = hash
		if stall >= l.StallRounds {
			finish("stalled", errors.New("verification output did not improve"))
			return
		}
	}
	finish("budget_exhausted", errors.New("verification did not pass within the round limit"))
}

// captureReport stores the agent's final message as a report artifact and parses the result contract.
func (a *App) captureReport(r *Run, dir, lastFile string, round, step int, out []byte) *AgentResult {
	name := fmt.Sprintf("round-%02d-step-%02d.report.md", round, step+1)
	text := ""
	if lastFile != "" {
		if b, err := os.ReadFile(lastFile); err == nil {
			text = string(b)
		}
	}
	if strings.TrimSpace(text) == "" {
		text, _ = finalMessage(r.Definition.Agent, out)
		if strings.TrimSpace(text) != "" {
			if os.WriteFile(filepath.Join(dir, name), []byte(text), 0600) != nil {
				return nil
			}
		}
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	r.Artifacts = append(r.Artifacts, name)
	if len(text) > 16<<10 {
		text = text[:16<<10] + "\n…"
	}
	r.Report = text
	res := parseResult(text)
	r.Result = res
	return res
}

// Time units for limits; tests shorten them.
var (
	wallUnit = time.Minute
	gateUnit = time.Hour
)

type gateDecision struct {
	approve bool
	note    string
}

func cancelStatus(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed_out"
	}
	return "cancelled"
}
func (a *App) Cancel(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role == "viewer" {
		return errors.New("viewer is read-only")
	}
	cancel, ok := a.active[id]
	if !ok {
		return errors.New("run is not active")
	}
	cancel()
	return nil
}
func (a *App) Decide(id string, approve bool, note string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Role == "viewer" {
		return errors.New("viewer is read-only")
	}
	gate, ok := a.gates[id]
	if !ok {
		return errors.New("run is not waiting at a gate")
	}
	if strings.TrimSpace(note) == "" {
		return errors.New("review note required")
	}
	if err := a.event(id, "gate.decision", fmt.Sprintf("approved=%t; %s", approve, note)); err != nil {
		return err
	}
	delete(a.gates, id)
	gate <- gateDecision{approve: approve, note: note}
	return nil
}
func (a *App) ArtifactPath(id, name string) (string, error) {
	r, err := a.Run(id)
	if err != nil {
		return "", err
	}
	found := false
	for _, n := range r.Artifacts {
		if n == name {
			found = true
		}
	}
	if !found || filepath.Base(id) != id || filepath.Base(name) != name {
		return "", errors.New("artifact not found")
	}
	path := filepath.Join(a.Dir, "runs", id, name)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(filepath.Join(a.Dir, "runs", id))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel != name {
		return "", errors.New("artifact escapes run directory")
	}
	return path, nil
}

func (a *App) ReadArtifact(id, name string) ([]byte, error) {
	if !validID.MatchString(id) || !validArtifactName.MatchString(name) {
		return nil, errors.New("invalid artifact path")
	}
	if a.Settings().Role == "viewer" {
		var e Evidence
		if err := a.get("shared_evidence", id, &e); err != nil {
			return nil, err
		}
		b, ok := e.Artifacts[name]
		if !ok {
			return nil, errors.New("artifact not found")
		}
		return []byte(b), nil
	}
	path, err := a.ArtifactPath(id, name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 8<<20 {
		return nil, errors.New("artifact too large")
	}
	return b, nil
}

func firstLine(s string, n int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	s = strings.TrimSpace(s) // drops a trailing \r from CRLF text
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
