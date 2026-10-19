---
name: loopbee
description: Turn a workflow into a scheduled loopbee loop, or inspect and run existing loops, through the local beectl CLI. Use when the user asks to run something nightly/daily/on a schedule, to "make this a loop", to hand a recurring agent task to loopbee, or to check what loopbee ran.
---

# loopbee loops

loopbee is a desktop app on this computer that owns scheduling. It runs agent CLIs (Codex CLI, Claude Code, OpenCode, pi) headlessly
on a cron schedule, records every step as evidence, pauses at human gates, and sends notifications.
You talk to it with `beectl` (installed next to the loopbee app). loopbee must be running.

## Commands

```sh
beectl status                      # app, agents, active runs
beectl loops                       # list loops (id, name, schedule, last result)
beectl runs --loop <id|name>       # recent runs of a loop
beectl schema                      # JSON schema of a loop definition, with an example
beectl create -f loop.json         # create a DISABLED draft (use -f - to read stdin; --dir DIR sets the project, default: current folder)
beectl run <id|name>               # start a run now (-q prints only the run ID)
beectl runs --status failed,blocked  # filter recent runs by status
beectl cancel <run-id> [--wait]    # cancel an active run (a unique ID prefix of 4+ characters works)
beectl cancel --loop <id|name>     # cancel the loop's active run
beectl delete <id|name> --yes      # delete a loop definition; its runs stay in history
```

Cancel or delete only what the user asked you to, naming the loop; never as clean-up on your own initiative.
When several loops share a name, beectl refuses and lists their IDs: ask the user which one.

`create` never enables a schedule. Tell the user to open loopbee, review the draft, run it once and then
switch the schedule on. Never try to enable it yourself.

## Writing a loop definition

```json
{
  "name": "Fix production errors",
  "description": "Nightly log sweep; stops before merging to master",
  "directory": "/absolute/path/to/repo",
  "agent": "codex",
  "cron": "0 2 * * *",
  "timezone": "Asia/Taipei",
  "sandbox": "workspace-network",
  "workspace": "worktree",
  "baseRef": "origin/master",
  "lock": "env:test",
  "maxRounds": 1,
  "wallMinutes": 90,
  "gateHours": 48,
  "steps": [
    {"kind": "act", "name": "Fix and deploy to test",
     "prompt": "Use the fix-prod-errors skill: query today's production errors, branch from origin/master, fix, merge to test and deploy, verify on test. Stop before merging to master."},
    {"kind": "gate", "name": "Developer approves production"}
  ],
  "notify": {"events": ["done", "attention", "failed"], "includeReport": true}
}
```

Rules:

- The team already has workflows and skills. Prefer ONE `act` step whose prompt names the skill or command to use
  and when to stop. loopbee appends its own result contract to every agent prompt; do not add one.
- Put a `gate` step before anything irreversible or shared (merging to main, production deploys, customer e-mail,
  SQL on shared databases). A gate as the last step means a person takes over.
- `run` steps execute a script without a shell (`argv` array). `verify` steps decide whether another round is
  needed by exit code. For both, `argv[0]` must be listed in `allowedTools`.
- `project` (optional): the name the boards group this loop under. Leave it out: loopbee uses the owner/repo of
  the directory's Git origin, or else the folder name.
- `agent`: `codex`, `claude`, `opencode` or `pi` (see `beectl status` for what is installed). OpenCode and
  pi have no sandbox: loopbee runs them only at `read-only` or `agent-config`. `model` is optional (empty = the
  agent's own default). Loop `effort` is optional too (empty = CLI default); use a level the agent offers.
  Agent steps (`act`, `observe`, `summarize`, `review`) may override `agent`, `sandbox`, `model` and `effort`;
  empty step fields inherit the Loop, then the CLI default for model/effort. The effective agent must enforce
  the effective permission, and step permissions must stay within the device ceiling.
- `sandbox`: the lowest that works — `read-only`, `workspace-write` (no network), `workspace-network` (git push,
  deploys, databases, APIs), `agent-config` (the agent's own configuration decides; only when asked). The device has
  a ceiling set in loopbee; drafts above it are rejected.
- `workspace: "worktree"` with a `branch` (e.g. `loopbee/fix-{date}`) when the loop changes code: loopbee creates the
  branch and commits after each agent step. Prompts must not tell the agent to branch, commit or push.
- To work from GitHub issues, set `"intake": {"kind": "github-issues"}` (optional `label`, `repo`, `trustedUsers`)
  and `"branch": "loopbee/issue-{issue}"`. loopbee picks one issue per run and gives it to the agent.
- To get a pull request, set `"pullRequest": {"mode": "after-review"}` with a `gate` after the agent step, or
  `{"mode": "draft"}` without one. loopbee pushes and opens it; never script that yourself. Add
  `"merge": "after-checks"` (after-review only) when the user wants it merged once CI is green. Use
  `"notify": {"channels": ["telegram"]}` to receive the link on the user's phone.
- Every run has the agent's own context: the user's skills, plugins, subagents, commands, CLAUDE.md / AGENTS.md and
  the project's memory. A skill that runs scripts needs at least `workspace-write`; one that calls services
  (APIs, databases, deployments, git push) needs `workspace-network`. A run without what it needs fails.
- `readPaths`: folders or files outside the project the workflow reads, e.g. a skill's credentials folder
  (`"readPaths": ["~/.pi/agent/private"]`). Read-only for the agent; credential stores such as ~/.ssh are refused.
- `copyFiles` (worktree only): Git-ignored project files the work needs, e.g. `[".env"]`. A fresh worktree has none;
  loopbee copies them from the checkout and never commits them. Install dependencies with a `run` step (`npm ci`).
- `agentMcp: true` when the work uses the agent's own MCP tools (a browser for a web console or an end-to-end
  test, a database, a ticket system). Nobody can approve a tool in an unattended run, so without it they are
  disabled. They act with the user's rights: put a gate before the step that changes something through them.
- `env` values may reference the user's shell: `{"name": "AWS_PROFILE", "value": "env:AWS_PROFILE"}`. loopbee
  looks in its own environment, then the login shell (Windows: the saved user variables).
- Never put secrets in the definition. Declare `"env": [{"name": "DB_PASSWORD", "secret": true}]` and tell the user to
  fill the value in loopbee (stored in the system keychain).
- `cron` is five fields in `timezone`; empty means manual only.

## Act, review and deliver

Use unique step `id` values (1–40 lowercase letters, digits or hyphens). `inputs` names earlier agent/review
steps whose latest reports are needed. A `review` returns `done` to pass or `changes_requested` with feedback;
`sendBackTo` must name an earlier `act`, `observe` or `summarize` step. `maxAttempts` bounds send-backs: 1–10
(omitted or 0 = 3). Writing steps need an agent enforcing the requested write level: Codex or Claude Code for
`workspace-write` / `workspace-network`, not pi/OpenCode at `read-only`.
An agent review is not a human approval: keep a `gate` after the last agent/review step before `after-review`
delivery. Commands still cannot run agent-changed files before human approval.

```json
{
  "name": "Fix, review and deliver",
  "directory": "/absolute/path/to/repo",
  "agent": "codex",
  "sandbox": "workspace-write",
  "workspace": "worktree",
  "branch": "loopbee/fix-{run}",
  "cron": "",
  "timezone": "UTC",
  "maxRounds": 1,
  "wallMinutes": 60,
  "stallRounds": 2,
  "doneWhen": "verify.passed",
  "steps": [
    {"id": "fix", "kind": "act", "name": "Fix", "prompt": "Follow AGENTS.md to fix the requested defect and test it. Do not branch, commit or push."},
    {"id": "review", "kind": "review", "name": "Review", "sandbox": "read-only", "inputs": ["fix"], "sendBackTo": "fix", "maxAttempts": 3, "prompt": "Review the changes and report actionable defects or pass."},
    {"id": "approve", "kind": "gate", "name": "Approve changes"},
    {"id": "deliver", "kind": "deliver", "name": "Open pull request", "pullRequest": {"mode": "after-review"}}
  ]
}
```

Use at most one `deliver` step and omit Loop-level `pullRequest` when using it. It opens the pull request at
that point, then continues with the following steps. Delivery needs a worktree and branch; `draft` mode needs
no gate. `merge: "after-checks"` requires `after-review`: it waits for passing checks and squash-merges before
continuing.
