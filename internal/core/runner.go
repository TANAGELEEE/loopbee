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
	"runtime"
	"slices"
	"strconv"
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
	var claimed *issueItem // the issue this Run labelled as being worked on
	issueWorked := false   // an agent has started on it; from then on the label stays until a person removes it
	finish := func(status string, err error) {
		if claimed != nil && !issueWorked {
			if e := releaseIssue(l, claimed.Number); e != nil {
				a.event(r.ID, "intake.release_failed", e.Error())
			} else {
				a.event(r.ID, "intake.released", fmt.Sprintf("#%d", claimed.Number))
			}
		}
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
		r.Phase = ""
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
	// Intake: take one work item before anything else; with none, the Run costs no agent call.
	var issue *issueItem
	if l.Intake != nil {
		ictx, icancel := context.WithTimeout(ctx, 2*time.Minute)
		item, skipped, err := pickIssue(ictx, l)
		icancel()
		if err != nil {
			finish("blocked", err)
			return
		}
		if item == nil {
			msg := fmt.Sprintf("No open issues labelled %s from people with write access.", l.Intake.label())
			if len(skipped) > 0 {
				msg += " Skipped: " + strings.Join(skipped, ", ") + "."
			}
			r.Result = &AgentResult{Status: "no_action", Summary: msg}
			a.event(r.ID, "intake.none", msg)
			finish("no_action", nil)
			return
		}
		issue = item
		claimed = item
		r.Issue = &item.IssueRef
		if b, err := json.MarshalIndent(item, "", "  "); err == nil && os.WriteFile(filepath.Join(dir, "issue.json"), b, 0600) == nil {
			r.Artifacts = append(r.Artifacts, "issue.json")
		}
		a.event(r.ID, "intake.issue", fmt.Sprintf("#%d %s", item.Number, item.Title))
	}
	issueNumber := 0
	if issue != nil {
		issueNumber = issue.Number
	}
	prepCtx, prepCancel := context.WithTimeout(ctx, 5*time.Minute)
	ws, err := a.prepareWorkspace(prepCtx, l, r.ID, issueNumber)
	prepCancel()
	if err != nil {
		if ctx.Err() != nil {
			finish(cancelStatus(ctx.Err()), ctx.Err())
			return
		}
		finish("blocked", err)
		return
	}
	workdir := ws.Dir
	r.Workdir = workdir
	r.Branch = ws.Branch
	agentRan := false // after an agent step, commands must not run files the agent changed (see below)
	env, err := loopEnv(l)
	if err != nil {
		finish("blocked", err)
		return
	}
	// What the agent uses on the developer's machine: its own skills, plugins and memory, and the Loop's read paths.
	home, _ := os.UserHomeDir()
	readPaths, err := resolveReadPaths(l, home)
	if err != nil {
		finish("blocked", err)
		return
	}
	agentReads := append(agentContextPaths(l.Agent, home, l.Directory), readPaths...)
	memory := "" // in a worktree the agent would not find the project's memory under its own folder name
	if m := agentMemoryDir(l.Agent, home, l.Directory); m != "" && l.Workspace == "worktree" {
		if _, err := os.Stat(m); err == nil {
			memory = m
		}
	}
	// Steps can hand data to later steps through files in LOOPBEE_RUN_DIR (never committed: it is outside
	// the worktree), e.g. the issue a polling script picked for a later "open a pull request" step.
	env = append(env, "LOOPBEE_RUN_ID="+r.ID, "LOOPBEE_RUN_DIR="+dir, "LOOPBEE_BRANCH="+r.Branch, "LOOPBEE_ISSUE="+strconv.Itoa(issueNumber))
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
	lastFailure := "" // the failed verification of the previous round, shown to the next round's agent
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
					// A person reviewed what the agent changed: later commands may run those files (a migration,
					// a deploy script). A later agent step makes the rule apply again.
					agentRan = false
					// Save the decision now: the page must stop offering the gate while later steps or the
					// delivery run, and a crash must not lose the approval.
					r.Status = "running"
					if l.PullRequest != nil && i == len(l.Steps)-1 {
						r.Phase = "delivering"
					}
					if err := a.saveRun(r); err != nil {
						a.event(r.ID, "storage.error", err.Error())
					}
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
			var argv, agentEnv []string
			var input io.Reader
			agentStep := s.Kind != "verify" && s.Kind != "run"
			lastFile := ""
			if !agentStep {
				argv = s.Argv
				// Command steps run as the user, outside any sandbox. Never let them execute a script the agent
				// wrote or changed in this Run (a malicious issue could otherwise reach the user's credentials).
				if agentRan && ws.Base != "" {
					changed, err := ws.changed(ctx)
					if err != nil {
						finish("failed", err)
						return
					}
					if f := ws.changedFileIn(argv, changed); f != "" {
						mark("failed", "refused: "+f+" was changed by the agent")
						finish("blocked", fmt.Errorf("command “%s” would run %s, which the agent changed in this run; review the branch instead", s.Name, f))
						return
					}
				}
			} else {
				agentRan = true
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
				inv := agentInvocation{Name: l.Agent, Dir: workdir, Sandbox: l.Sandbox, Model: l.Model, LastFile: lastFile, ReadPaths: agentReads, Scratch: dir, MCP: l.AgentMCP, Home: home, Project: l.Directory}
				if l.AgentMCP || l.Sandbox != "agent-config" {
					inv.MCPList, inv.MCPListed = listAgentMCP(ctx, l.Agent, base, workdir, env)
				}
				argv, agentEnv = buildAgentInvocation(base, ag.Caps, inv)
				issueWorked = true
				prompt := s.Prompt + readablePrompt(readPaths, memory)
				if r.Branch != "" {
					prompt += "\n\nYou are on branch " + r.Branch + " in a fresh worktree that loopbee created from " + orDefault(l.BaseRef, "HEAD") + ". Do not create or switch branches and do not commit: loopbee commits your changes on this branch after this step."
				}
				if issue != nil {
					prompt += issuePrompt(issue)
				}
				if lastFailure != "" {
					prompt += "\n\nThe previous round's verification failed. Fix this first:\n" + lastFailure
				}
				if earlier.Len() > 0 {
					prompt += "\n\nOutput of the earlier steps of this run (captured by loopbee; use it instead of re-running the commands):\n" + earlier.String()
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
					kind, failed = classifyFor(l.Agent, line)
					agentFailed = agentFailed || failed
				}
				if e := a.event(r.ID, kind, line); e != nil {
					eventErr = e
				}
			}, input, append(append([]string{}, env...), agentEnv...))
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
				if u := usageFor(l.Agent, out, l.Model, home); u != nil {
					r.Trace[len(r.Trace)-1].Usage = u
					if r.Usage == nil {
						r.Usage = &Usage{}
					}
					r.Usage.add(*u)
				}
				if agentFailed {
					if reason := failureFor(l.Agent, out, runtime.GOOS); reason != "" {
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
				note := ""
				if res != nil {
					note = res.Summary
				}
				if r.Branch != "" {
					subject := l.Name + ": " + s.Name
					if res != nil && res.Summary != "" {
						subject = l.Name + ": " + firstLine(res.Summary, 120)
					}
					if res != nil && res.Status == "blocked" {
						subject = l.Name + " (unfinished): " + firstLine(res.Summary, 120)
					}
					cctx, ccancel := context.WithTimeout(ctx, 2*time.Minute)
					sha, err := ws.commit(cctx, subject+"\n\nCommitted by loopbee for run "+r.ID+".")
					var patch []byte
					if err == nil {
						// The reviewer at the gate sees everything on the branch since the last review point: this
						// step's commit and, on a continued branch, the commits of earlier Runs.
						patch, _ = ws.git(cctx, "log", "--no-ext-diff", "--stat", "--patch", "--format=fuller", ws.Reviewed+"..HEAD")
						if head, e := ws.git(cctx, "rev-parse", "HEAD"); e == nil {
							ws.Reviewed = strings.TrimSpace(string(head))
						}
					}
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
					if len(bytes.TrimSpace(patch)) > 0 {
						name := fmt.Sprintf("round-%02d-step-%02d.diff", round, i+1)
						if len(patch) > 2<<20 {
							patch = append(patch[:2<<20], []byte("\n… (truncated)\n")...)
						}
						if e := os.WriteFile(filepath.Join(dir, name), patch, 0600); e == nil {
							r.Artifacts = append(r.Artifacts, name)
						}
						if found := secretsInPatch(patch); len(found) > 0 {
							mark("failed", "possible credentials in the change")
							finish("failed", fmt.Errorf("the agent's change on %s contains what looks like a credential (%s); nothing was pushed — review and delete the branch", r.Branch, strings.Join(found, "; ")))
							return
						}
					}
				}
				if res != nil && res.Status == "no_action" && ws.Branch != "" && ws.hasWork(ctx) {
					// A continued branch already holds unreviewed work from an earlier Run: “nothing more to
					// change” still means that work goes to review and delivery, not that the Run ends.
					res.Status = "done"
					note = strings.TrimSpace(note + " · " + r.Branch + " already has the change")
					a.event(r.ID, "workspace.existing_work", r.Branch)
				}
				if res != nil {
					switch res.Status {
					case "no_action":
						mark("ok", note)
						finish("no_action", nil)
						return
					case "blocked":
						msg := res.Summary
						if msg == "" {
							msg = "agent reported that it is blocked"
						}
						// Anything the agent changed is already committed above, so a retry continues from it.
						finish("blocked", errors.New(msg))
						return
					}
				}
				mark("ok", note)
				a.event(r.ID, "step.completed", s.Name)
				continue
			}
			if s.Kind == "run" && exitCode(err) == NothingToDoExit {
				// A polling script found nothing to do: end quietly without calling the agent.
				msg := firstLine(string(out), 200)
				mark("ok", msg)
				r.Result = &AgentResult{Status: "no_action", Summary: msg}
				finish("no_action", nil)
				return
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
					lastFailure = fmt.Sprintf("### %s — `%s` (%s)\n```\n%s\n```\n", s.Name, strings.Join(argv, " "), err.Error(), tail(string(out), 6000))
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
			note := ""
			if s.Kind == "run" {
				// A command can report too (e.g. the pull request it opened): its summary and links are added to the
				// Run's result, so notifications carry them. Only agents decide the outcome.
				if res := parseResult(string(out)); res != nil {
					note = res.Summary
					if r.Result == nil {
						r.Result = &AgentResult{Status: "done"}
					}
					if res.Summary != "" {
						r.Result.Summary = strings.TrimSpace(r.Result.Summary + "\n" + res.Summary)
					}
					for _, link := range res.Links {
						if !slices.Contains(r.Result.Links, link) {
							r.Result.Links = append(r.Result.Links, link)
						}
					}
				}
			}
			mark("ok", note)
			a.event(r.ID, "step.completed", s.Name)
		}
		done, err := condition(l.DoneWhen, r.Metrics, verified)
		if err != nil {
			finish("failed", err)
			return
		}
		if verified && done {
			if l.PullRequest != nil {
				if r.Phase != "delivering" {
					r.Phase = "delivering"
					if err := a.saveRun(r); err != nil {
						a.event(r.ID, "storage.error", err.Error())
					}
				}
				a.event(r.ID, "delivery.started", r.Branch)
				pctx, pcancel := context.WithTimeout(ctx, 5*time.Minute)
				url, err := deliverPullRequest(pctx, l, r, ws, reviewerNote)
				pcancel()
				if err != nil {
					finish("failed", fmt.Errorf("pull request: %w", err))
					return
				}
				r.PRURL = url
				if r.Result == nil {
					r.Result = &AgentResult{Status: "done"}
				}
				label := "Pull request ready for review and merge"
				if l.PullRequest.Mode == "draft" {
					label = "Draft pull request ready for review"
				}
				r.Result.Summary = strings.TrimSpace(r.Result.Summary + "\n" + label + ": " + url)
				if !slices.Contains(r.Result.Links, url) {
					r.Result.Links = append(r.Result.Links, url)
				}
				a.event(r.ID, "delivery.pull_request", url)
				if l.PullRequest.Merge == "after-checks" {
					head, _ := ws.git(ctx, "rev-parse", "HEAD")
					r.Phase = "merging"
					merged, why, err := mergeWhenGreen(ctx, l, r, url, strings.TrimSpace(string(head)), func() {
						if err := a.saveRun(r); err != nil {
							a.event(r.ID, "storage.error", err.Error())
						}
					})
					if err != nil {
						if ctx.Err() != nil {
							finish(cancelStatus(ctx.Err()), ctx.Err())
							return
						}
						why = err.Error()
					}
					if !merged {
						// The pull request stays open: the reviewer decides, with the reason on their phone.
						r.Result.Summary = strings.TrimSpace(r.Result.Summary + "\nNot merged — " + why)
						a.event(r.ID, "delivery.not_merged", why)
						finish("blocked", fmt.Errorf("pull request not merged: %s", why))
						return
					}
					r.Merged = true
					r.Result.Summary = strings.Replace(r.Result.Summary, label+": "+url, "Merged after checks passed: "+url, 1)
					a.event(r.ID, "delivery.merged", url)
				}
			}
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
	if res != nil {
		// What the CLI refused for want of an approval, or the sandbox stopped, even when the agent did not say so.
		mergeMissing(res, mcpRefused(r.Definition.Agent, out, r.Definition.AgentMCP))
		mergeMissing(res, sandboxBlocked(out))
	}
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

// NothingToDoExit is the exit code a command step uses to say "nothing to do": the Run ends as no_action
// instead of failing, and later steps (agents, gates) do not run. 78 follows the CI convention for a
// neutral result.
const NothingToDoExit = 78

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
