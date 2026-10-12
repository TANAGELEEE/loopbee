# Data and implementation contracts

## Loop

`id` is a random 24-character hexadecimal ID. `version` increments on save; updates supply the old version and stale edits are rejected. `steps` contains 1–50 observe, act, verify, gate, or summarize steps. Agent steps require a prompt; verify requires nonempty string argv with argv[0] in allowedTools; gate requires a human decision.

`maxRounds` (1–20), `wallMinutes` (1–1440), and `stallRounds` (1–20) are mandatory limits. Only time spent executing steps counts toward `wallMinutes`; waiting at a gate does not. A gate expires after `gateHours` (default 72, max 336) and the Run ends as timed_out. Timezones use IANA names, and the application embeds tzdata. Cron expressions have five fields; an empty cron means manual execution only. `enabled=false` prevents scheduled execution but still permits manual Runs. An empty `lock` prevents concurrent runs of the same Loop; a nonempty lock also provides mutual exclusion across Loops.

Permission levels, lowest to highest: read-only, workspace-write, workspace-network, agent-config (see [agents](subsystems/agents.md)). The device ceiling defaults to read-only; raising it requires a typed confirmation, and Loops above the ceiling cannot be saved or started. RequireClean blocks execution in direct mode when Git status --porcelain is nonempty or the directory is not a Git repository. `workspace: "worktree"` creates a fresh detached git worktree under `<data>/worktrees/<loop>` from `baseRef` (after fetching the remote) for every Run, leaving the developer's checkout untouched. With `branch`, the worktree checks out that new branch instead and Loopbee commits the agent's changes after each agent step (outside the agent sandbox, without running hooks, after checking the worktree's `.git` file; the diff is stored and scanned for credentials); an existing branch of the same name blocks the Run rather than being overwritten, shared names such as master/main are rejected, and Loopbee never pushes or merges.

**Intake and delivery** are Loop settings run by Loopbee with the user's GitHub CLI login. `intake: {kind: "github-issues"}` takes, per Run and before the workspace is created, the oldest open issue with the ready label (default `agent-ready`) that was opened by an OWNER/MEMBER/COLLABORATOR or a listed login and is not labelled working (default `agent-working`); Loopbee adds the working label, records `run.issue`, fills `{issue}` in the branch name and appends the issue — trusted comments only, fenced as untrusted text — to every agent prompt. No such issue ends the Run as `no_action` without an agent call. `pullRequest: {mode}` runs when the Run completes: it requires commits, refuses changes under `.github/`, pushes the branch without hooks and opens the pull request (`after-review` needs a gate after the last agent step; `draft` opens a draft). The link is stored as `run.prUrl` and added to the result, so notifications carry it. Both need a worktree and a branch.

After an agent step, a command step whose arguments name a file the agent changed is refused (`blocked`). Every step gets `LOOPBEE_LOOP`, `LOOPBEE_RUN_ID`, `LOOPBEE_RUN_DIR` (the Run's artifact folder, outside the worktree, for state passed between steps) and `LOOPBEE_BRANCH`. A command (`run`) step that exits with **78** ends the Run as `no_action` before any later step, so polling Loops cost no agent call when there is nothing to do. When a verification fails, its output is appended to the next round's agent prompt. `env` variables are injected into agent and command steps; secret values are stored in the system keychain and the definition keeps `keyring:`/`env:` references only. A secret without a value blocks the Run.

## Run / Event / Artifact

States: queued → running ↔ waiting → completed / no_action / failed / blocked / rejected / cancelled / timed_out / budget_exhausted / stalled. Step kinds: act/observe/summarize (agent), run (allowlisted command; failure fails the Run), verify (allowlisted command; failure starts the next round), gate. Active Runs discovered on restart become interrupted. Terminal Runs cannot be approved again; rerunning creates a separate Run.

Events are append-only and preserve raw agent JSONL. Known types are labeled `agent.<original-type>`; unknown or non-JSON lines become `agent.raw` rather than being discarded. The UI and shared snapshots return the latest 1,000 events. Older events remain in SQLite until the Run expires and is pruned. Combined stdout+stderr is limited to 8 MiB per step. Exceeding the limit fails the step and preserves collected evidence. Event/file write failures fail the Run rather than reporting success.

Each step writes `round-NN-step-NN.txt`. The app does not automatically collect arbitrary workspace files or expose local paths in team snapshots. A Run's Artifact list contains only successfully written files.

## Completion and retries

A Run completes only when every step succeeds and doneWhen is satisfied. Expressions support `metrics.name`, `verify.passed`, numeric comparisons, Boolean constants, parentheses, &&, ||, and !. Calls, indexing, and arbitrary code execution are unsupported. An empty condition means verify.passed; unknown metrics are unsatisfied. When verify specifies metric and metricPath (for example, $.summary.score), command output must be pure JSON and the selected value must be numeric; incorrect types are not coerced. Verification stdout/stderr is combined as evidence, so do not mix non-JSON logs into metric output.

A nonzero verify exit or unmet condition starts another round from the first step, including any earlier human gates. Agent-step errors or turn.failed events immediately fail the Run. Time limits immediately terminate the process tree. With metrics, stalling compares metric changes; otherwise it compares whole-round output. Reaching stallRounds consecutive identical results stops execution. An agent's natural-language claim of completion does not determine the outcome.

## Persistence and archives

SQLite `objects(kind,id,data)` stores versioned JSON entities under primary key `(kind,id)`; `events` is append-only. Local and `shared_*` namespaces are isolated, so role changes do not delete local data. Retention removes only terminal Runs older than the configured number of days, together with their events and files. Running and waiting Runs are retained.

ZIP manifest version is 1. Exports contain only non-private Loops, terminal non-private Runs, the latest 1,000 events, and text artifacts. Matches for common credentials or email addresses block export. Imports validate extraction limits, registered artifacts, and paths before staging in the application directory and importing in a database transaction. They receive new IDs, and Loops are private and disabled; existing data is never overwritten.

## Explicit differences from the richer prototype

The implementation does not include targeted partial loops, automatic rollback, Loop-specific business branches, path scope / deny_paths, agent tool lists, dry runs, drag-and-drop ordering, external cc-nexs gates, or manual per-file publish overrides. JSON paths support dotted object paths only, without arrays, wildcards, or filters.

- Verification exit codes and restricted completion expressions drive full-round retries. Accessible move-up/move-down buttons handle ordering.
- Cancellation kills processes without reverting files. Operators remain responsible for branch/workspace management.
- CLI sandboxes constrain workspace permissions. Finer path-level/tool-level enforcement is not claimed.
- No cc-nexs commands are sent and no external approvals are granted. A gate is Loopbee's own human checkpoint.
- Digests aggregate by an independent timezone, support copying, and have their own Cron delivery schedule. Automatic delivery defaults to off.
- Claude scheduled-tasks are shown as read-only source text. Without a verified source format, the app does not guess mappings, write back, or enable tasks automatically. Manually converted JSON/YAML can be imported into the editor.

These extensions are unfinished and are not counted as accepted functionality. The Windows desktop shell was implemented and passed local automated tests on 2026-10-11; see [platform acceptance](subsystems/gui-shell.md) for the scope. Linux desktop support, signing, notarization, automatic updates, and worktrees remain outside v1.

## UI spec items not yet implemented (updated 2026-10-11, third pass)

Done since the review: agent adapters for Codex and Claude Code, four permission levels, result contract and reports, `no_action`, gate expiry outside the time budget, command steps, environment variables and keychain secrets, git worktrees, per-Loop notifications with report content, login-shell PATH, default agent, chat authoring, beectl and the agent skill.

Still open, in rough priority order:

1. Tray menu states and items (running Runs, “Pause all schedules”), and the quit-with-active-runs dialog.
2. `loopbee://` URL scheme and deep links from desktop notifications.
3. Gate review with per-item keep/drop checklists (needed for the CRM e-mail scenario).
4. Definition version list, diff and restore (snapshots are viewable from Runs).
5. Editor: YAML view, drag-and-drop ordering, loop ranges, trial runs.
6. Budget-exhausted “continue (add N rounds)”; only “Run again” exists.
7. Stored daily digests per date and “regenerate”.
8. Markdown rendering and paging for large artifacts; reveal in Finder/Explorer.
9. Close-window behaviour and global concurrency limit as settings.
10. Remote approval (phone/Feishu) — deliberately out of scope: decisions happen on the executor's computer.
11. axe accessibility automation and WebKit screenshot runs.
