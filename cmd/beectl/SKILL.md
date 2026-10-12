---
name: loopbee
description: Turn a workflow into a scheduled Loopbee loop, or inspect and run existing loops, through the local beectl CLI. Use when the user asks to run something nightly/daily/on a schedule, to "make this a loop", to hand a recurring agent task to Loopbee, or to check what Loopbee ran.
---

# Loopbee loops

Loopbee is a desktop app on this computer that owns scheduling. It runs Codex CLI or Claude Code headlessly
on a cron schedule, records every step as evidence, pauses at human gates, and sends notifications.
You talk to it with `beectl` (installed next to the Loopbee app). Loopbee must be running.

## Commands

```sh
beectl status                      # app, agents, active runs
beectl loops                       # list loops (id, name, schedule, last result)
beectl runs --loop <id|name>       # recent runs of a loop
beectl schema                      # JSON schema of a loop definition, with an example
beectl create -f loop.json         # create a DISABLED draft (use -f - to read stdin; --dir DIR sets the project, default: current folder)
beectl run <id|name>               # start a run now
```

`create` never enables a schedule. Tell the user to open Loopbee, review the draft, run it once and then
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
  and when to stop. Loopbee appends its own result contract to every agent prompt; do not add one.
- Put a `gate` step before anything irreversible or shared (merging to main, production deploys, customer e-mail,
  SQL on shared databases). A gate as the last step means a person takes over.
- `run` steps execute a script without a shell (`argv` array). `verify` steps decide whether another round is
  needed by exit code. For both, `argv[0]` must be listed in `allowedTools`.
- `sandbox`: the lowest that works — `read-only`, `workspace-write` (no network), `workspace-network` (git push,
  deploys, databases, APIs), `agent-config` (the agent's own configuration decides; only when asked). The device has
  a ceiling set in Loopbee; drafts above it are rejected.
- `workspace: "worktree"` with a `branch` (e.g. `loopbee/fix-{date}`) when the loop changes code: Loopbee creates the
  branch and commits after each agent step. Prompts must not tell the agent to branch, commit or push.
- To work from GitHub issues, set `"intake": {"kind": "github-issues"}` (optional `label`, `repo`, `trustedUsers`)
  and `"branch": "loopbee/issue-{issue}"`. Loopbee picks one issue per run and gives it to the agent.
- To get a pull request, set `"pullRequest": {"mode": "after-review"}` with a `gate` after the agent step, or
  `{"mode": "draft"}` without one. Loopbee pushes and opens it; never script that yourself. Add
  `"merge": "after-checks"` (after-review only) when the user wants it merged once CI is green. Use
  `"notify": {"channels": ["telegram"]}` to receive the link on the user's phone.
- Never put secrets in the definition. Declare `"env": [{"name": "DB_PASSWORD", "secret": true}]` and tell the user to
  fill the value in Loopbee (stored in the system keychain).
- `cron` is five fields in `timezone`; empty means manual only.
