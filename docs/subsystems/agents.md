# Local agents

Loopbee drives two local CLIs headlessly: **Codex CLI** and **Claude Code**. Both are detected at start-up and on demand (Settings › Agents › Detect again) through PATH, `LOOPBEE_CODEX_BIN` / `LOOPBEE_CLAUDE_BIN` (an executable or a `.js`/`.mjs` entry point) or, on Windows, the npm `.cmd` wrapper's `node_modules` entry point. Before detection Loopbee merges the PATH of the user's login shell (`$SHELL -ilc`), so agents installed through nvm or Homebrew are found when the app starts from the Dock or Finder.

Detection runs `--version` and the help text (`codex exec --help`, `claude --help`) and records capabilities. Optional flags are only passed when the help text lists them. `available` means the headless flags exist; it does not prove that a model, account or quota is usable.

The first available agent becomes the **default agent** (`settings.defaultAgent`). It drafts Loops in the chat and is preselected for new Loops; each Loop can still choose its own agent.

## Invocation

Prompts always go through stdin. Every agent step gets the Loop prompt, the reviewer note from the previous gate (if any) and the result contract below.

| Permission level | Codex | Claude Code |
|---|---|---|
| read-only | `exec --json --sandbox read-only` | `-p --output-format stream-json --verbose --permission-mode dontAsk --tools Read,Grep,Glob` |
| workspace-write | `--sandbox workspace-write` | `--permission-mode acceptEdits --settings {"sandbox":{"enabled":true,"autoAllowBashIfSandboxed":true,"allowUnsandboxedCommands":false,"failIfUnavailable":true,"network":{"allowedDomains":[]}}}` |
| workspace-network | `--sandbox workspace-write -c sandbox_workspace_write.network_access=true` | as above with `"allowedDomains":["*"]` |
| agent-config | no sandbox flags (config.toml / `--profile` decide) | no permission flags (settings.json decides) |

Codex also gets `-C <workdir>`, `--skip-git-repo-check`, `--output-last-message <report file>` and, for worktrees, `--add-dir <git common dir>` so commits from the worktree can be written. Claude Code gets `--add-dir` likewise. The device ceiling (`settings.maxSandbox`) caps every Loop; raising it needs a typed confirmation.

Claude Code's Bash sandbox runs on macOS, Linux and WSL2 only. With `failIfUnavailable` Claude Code refuses to start on native Windows or when bubblewrap/socat are missing (verified locally: the run fails with “Sandbox required but unavailable … bubblewrap (bwrap) not installed”). On native Windows use Codex, WSL2, or the agent-config level for Claude Code.

## Git work in worktrees

Codex's workspace-write sandbox makes Git metadata read-only, including a worktree's own gitdir: on the real CLI (0.162.1, macOS) the agent could create a branch ref but failed on `HEAD.lock` and could not switch or commit. Claude Code's sandbox allowed it. So for both CLIs Loopbee does the Git work itself: with `branch` set it creates the branch in the fresh worktree, tells the agent not to branch or commit, and commits all changes after each agent step as `<Loop name>: <result summary>`. The Run records `branch` and `commits`; notifications include them.

Agents get **no** write access to Git metadata (no `--add-dir` for the common Git directory). Loopbee's own Git commands in a worktree run without hooks (`core.hooksPath` → an empty folder, `commit --no-verify`) and without fsmonitor, and stop if the worktree's `.git` file changed — otherwise an agent could plant a hook (e.g. in `.husky/`) that Loopbee would execute as the user. Each commit is stored as `round-NN-step-NN.diff` for the reviewer and scanned for credentials (private keys, `sk-`, `ghp_`/`github_pat_`, `AKIA`, Slack tokens); a match fails the Run. A command step that would run a file the agent changed in this Run is refused. Pushing and merging stay with the agent (workspace-network, if the user asks) or with a person after the gate.

## Command output for agent steps

A failed verification's output (last 6 KB) is added to the next round's agent prompt, so a retry is informed. A command step exiting 78 ends the Run as “nothing to do” before any agent call. See [issue-driven iteration](../workflows/issue-loop.md) for a Loop built on both.

The output of `run` and `verify` steps (last 6 KB each, with the exit status) is appended to the prompt of later agent steps in the same round. A `run` step with `continueOnError` records a non-zero exit as a failed step and continues, so an agent can report on failing tests or log queries. This matters most for Claude Code at read-only, which has no shell at all: the chat designer is told so and puts commands in a `run` step before the agent step (verified with the real CLI).

## Result contract

Loopbee appends: end the final message with `LOOPBEE_RESULT {"status":"done|no_action|blocked|needs_approval","summary":"…","links":["…"]}`. The final message (Codex `--output-last-message`, else the last `agent_message`; Claude's `result` event) is stored as `round-NN-step-NN.report.md`, shown in Run details and optionally sent with notifications. `no_action` ends the Run as “Nothing to do” and skips the remaining steps (including gates); `blocked` ends it as blocked with the agent's summary; `done` and `needs_approval` continue. Without a result line the step counts as done when the CLI exits successfully. Failed turns (`turn.failed`, Claude `is_error`) fail the Run with the CLI's own error text.

Event types: Codex `thread.*`, `turn.*`, `item.*`, `error`; Claude `system`, `assistant`, `user`, `stream_event`, `result`. Everything else is stored as `agent.raw`.

## Chat authoring

`POST /api/author` runs the chosen agent once, **read-only**, inside the target project directory (or an empty scratch directory) with the conversation, the current draft and a JSON schema (`--output-schema` for Codex, `--json-schema` for Claude Code). The answer is normalised: identity, directory, agent, enabled state and secret values stay under Loopbee's control, a proposal never enables a schedule, and `Validate` plus the device ceiling are checked before the user can save. Each message is a real CLI call (typically 10–60 s, 4 min limit) on the user's own subscription.

## beectl and the skill

`beectl` talks to the running app over the loopback API with the launch token. `beectl skill install` writes `SKILL.md` to `~/.claude/skills/loopbee`, `~/.codex/skills/loopbee` and `~/.agents/skills/loopbee`, so an agent can create **disabled, private** drafts with `beectl create -f -`. Secrets in definitions are refused; drafts must be reviewed, run once and enabled in the app.

## Real smoke test

`tests/real-smoke.mjs` (or `sh scripts/real-smoke.sh` on a Mac, which also runs `go test` and packages the app) drives the packaged binary with the real CLIs against a throw-away Git repository with a bare origin. Per agent: **report** (chat → read-only Loop → report naming the TODO and the failing test) and **fix** (chat → worktree Loop at workspace-write → branch with a committed fix → stops at the gate → developer checkout untouched, nothing pushed → approve → done). It uses the user's subscription for a few short runs.

Results so far (Linux container, Claude Code 2.1.296): chat authoring 10–30 s with schema output; **report** passes end to end. **fix** fails there only because bubblewrap is missing, with the CLI's own error surfaced in the Run. The first real run found two defects that the fakes could not: drafts defaulted to UTC (the chat now sends the browser time zone) and Claude Code at read-only could not run `npm test` (now a `run` step with `continueOnError`).

Results on macOS 27.0 arm64 (packaged `Loopbee.app`, Codex CLI 0.162.1, Claude Code 2.1.296, 2026-10-11): `go test ./...` and packaging pass; **Codex report**, **Claude report** and **Claude fix** pass end to end (branch `loopbee/fix-tests` with the committed fix, checkout untouched, nothing pushed, gate approval completes the Run). **Codex fix** was not run in the first pass: the Codex designer chose workspace-network, above the test's device limit. The designer is now told the device limit and that Loopbee fetches `baseRef` itself, and the chat explains how to raise the limit when a draft exceeds it. In the second pass Codex chose workspace-write correctly but was blocked by its Git sandbox (see “Git work in worktrees”); Loopbee-managed branches were added in response.

Subscription limits surface as readable errors: Claude Code reports `is_error` with `api_error: usage_limit_reached` and a text such as “You've hit your session limit · resets 10:20pm”, which Loopbee now shows verbatim in the chat and in failed Runs.

## Not verified on real CLIs

The adapters are covered by fake CLIs in Go and browser tests and by the real smoke test above. Codex flags follow the official CLI reference but were not executed here. Before relying on them, run one Loop per permission level on each machine type and keep the captured output as fixtures. Whether Codex reads skills from `~/.codex/skills` or `~/.agents/skills` depends on the installed version; `beectl skill install` writes both.
