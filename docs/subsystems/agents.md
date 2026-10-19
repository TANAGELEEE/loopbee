# Local agents

loopbee drives local agent CLIs headlessly through **adapters**, one Go file per CLI (`internal/core/agent_*.go`,
registry in `adapter.go`): **Codex CLI**, **Claude Code**, **OpenCode** and **pi** (Gemini CLI was supported from 2026-10-13 and removed on 2026-10-16). Adapters are
compiled in; there is no plugin host. Each is detected at start-up and on demand (Settings › Agents › Detect again)
through PATH, `LOOPBEE_<NAME>_BIN` (an executable or a `.js`/`.mjs`/`.cjs` entry point) or, on Windows, the npm `.cmd`
wrapper's `node_modules` entry point. Before detection loopbee merges the PATH of the user's login shell
(`$SHELL -ilc`), so agents installed through nvm or Homebrew are found when the app starts from the Dock or Finder.

Detection runs `--version` and the adapter's help command (`codex exec --help`, `claude --help`,
`opencode run --help`, `pi --help`) and records capabilities. Optional flags are only passed when the help text lists
them. `available` means the headless flags exist; it does not prove that a model, account or quota is usable. When a
CLI is found but a detection command fails or times out, the agent is `unavailable` with a `problem` that names the
command and the fix (a process killed at once, exit 137, is usually a damaged install or one macOS blocks); Settings ›
Agents shows it as “Not working” instead of “Not detected”.

The first available agent (in the order above) becomes the **default agent** (`settings.defaultAgent`). It drafts
Loops in the chat and is preselected for new Loops; each Loop can still choose its own agent and, optionally, a
`model` (passed as `--model`; empty = the CLI's own default).

## Models and effort

Each agent step can override `model` and `effort` independently: **step → Loop → CLI default**. Empty fields
inherit the next level. Settings › Agents lists the available choices; the Run's flow strip shows each step's
model and effort.

| CLI | Effort flag and offered levels | Model choices |
|---|---|---|
| Claude Code | `--effort`: low, medium, high, xhigh, max | aliases from `--help` |
| pi | `--thinking`: off, minimal, low, medium, high, xhigh, max | `pi --list-models` |
| Codex | `-c model_reasoning_effort=…`; no levels offered because the CLI does not tell which it accepts | none listed |
| OpenCode | none; variants are not listed | `opencode models`, with one retry if the first listing fails or is empty |

A failure to list models never fails agent detection. Models may still be entered directly; an empty model uses
the CLI's own default.

OpenCode's list comes from its background service and depends on how far that service has loaded its providers: on
the developer's Mac (2026-10-19) the same `opencode models` returned 0, 12, 84 and 184 models at different times,
and `--standalone` returned none. Detection keeps whatever it got, so the list can be partial; “List models again”
in Settings › Agents asks again, and any model name can still be typed.

## Permission levels are a promise

A permission level tells the person reading a Loop what the agent can do unattended. Each adapter lists the levels
its CLI **enforces itself**; any other level with that agent is refused when the Loop is saved or started, with the
levels it does offer — never run with less protection.

| Agent | Levels | How they are enforced |
|---|---|---|
| Codex CLI | all four | its OS sandbox (`--sandbox`) |
| Claude Code | all four | tool limits at read-only; its OS sandbox for Bash at write levels (macOS, Linux, WSL2) |
| OpenCode | read-only, agent-config | its permission settings via `OPENCODE_CONFIG_CONTENT`; no sandbox exists |
| pi | read-only, agent-config | the read tools alone (`--tools read,grep,find,ls`); pi has no sandbox or prompts |

## Invocation

Prompts always go through stdin. Every agent step gets the Loop prompt, the reviewer note from the previous gate (if
any) and the result contract below.

| Permission level | Codex | Claude Code |
|---|---|---|
| read-only | `exec --json --sandbox read-only` | `-p --output-format stream-json --verbose --permission-mode dontAsk --tools Read,Grep,Glob,Skill` (+ `--settings` with read rules, see below) |
| workspace-write | `--sandbox workspace-write` | `--permission-mode acceptEdits --settings {"sandbox":{"enabled":true,"autoAllowBashIfSandboxed":true,"allowUnsandboxedCommands":false,"failIfUnavailable":true,"network":{"allowedDomains":[]}}}` |
| workspace-network | `--sandbox workspace-write -c sandbox_workspace_write.network_access=true` | as above with `"allowedDomains":["*"]` |
| agent-config | no sandbox flags (config.toml / `--profile` decide) | no permission flags (settings.json decides) |

| Permission level | OpenCode (`run --format json`) | pi (`--mode json --no-session`) |
|---|---|---|
| read-only | `OPENCODE_CONFIG_CONTENT={"permission":{"edit":"deny","bash":"deny","webfetch":"deny","websearch":"deny","codesearch":"deny","task":"deny","external_directory":{"*":"deny","<read path>/**":"allow"}}}` | `--tools read,grep,find,ls` |
| workspace-write | refused | refused |
| workspace-network | refused | refused |
| agent-config | no permission settings (non-interactive “ask” is rejected by OpenCode) | no tool flags |

Codex also gets `-C <workdir>`, `--skip-git-repo-check`, `--output-last-message <report file>` and, for worktrees, `--add-dir <git common dir>` so commits from the worktree can be written. Claude Code gets `--add-dir` likewise. The device ceiling (`settings.maxSandbox`) caps every Loop; raising it needs a typed confirmation.

MCP tools are a separate switch on top of these flags; see [The agent's own MCP tools](#the-agents-own-mcp-tools).

Claude Code's Bash sandbox runs on macOS, Linux and WSL2 only. With `failIfUnavailable` Claude Code refuses to start on native Windows or when bubblewrap/socat are missing (verified locally: the run fails with “Sandbox required but unavailable … bubblewrap (bwrap) not installed”; on Windows the CLI says the Windows sandbox is “not active on this session (feature gate off)”). On native Windows use Codex, WSL2, or the agent-config level for Claude Code. The failed Run says so: Claude Code's own advice to set `sandbox.failIfUnavailable=false` has no effect because loopbee passes the setting, so loopbee appends what does work. The editor warns about Claude Code at a write level on Windows, and the chat designer is told the computer's platform.

## What a run can reach

A workflow that works in the developer's own session must not fail in a Loop because the Loop cannot see what the
session used. loopbee gives every run:

- **The agent's own context.** Skills, plugins, subagents, commands and user instructions are loaded by the CLI
  itself; loopbee also allows reading them, so the agent can open a skill's scripts and references: for Claude Code
  `~/.claude/{skills,plugins,agents,commands,CLAUDE.md}` (or `CLAUDE_CONFIG_DIR`) and the project's memory
  `~/.claude/projects/<project path with non-alphanumerics as ->/memory`; for Codex `~/.codex/{skills,prompts,memories,AGENTS.md}`
  (or `CODEX_HOME`); for both `~/.agents/skills`. Only paths that exist are passed. Claude Code gets them as
  `Read(//abs/path/**)` allow rules in `--settings` (Windows paths as `//c/Users/...`); Codex's sandboxes already read
  the whole disk on macOS and Linux. At read-only Claude Code also gets the `Skill` tool, so it can load a skill
  (without a shell its scripts still cannot run). In a worktree the prompt points at the project's memory, which the
  agent would otherwise look for under the worktree's path.
- **Read paths** (`readPaths`): folders or files outside the project that the Loop lists, e.g. a skill's
  credentials folder (`~/.pi/agent/private`). Read-only, added to the rules above and named in the prompt. Validation
  refuses the home folder itself, its parents and the computer's credential stores (`~/.ssh`, `~/.gnupg`, `~/.aws`,
  `~/.azure`, `~/.kube`, `~/.docker`, `~/.netrc`, `~/.git-credentials`, `~/.config/gh`, `~/.config/gcloud`,
  `~/Library/Keychains`); a path missing on this computer blocks the Run with that reason. The editor warns when a
  Loop that works on other people's issues can read such paths.
- **Git-ignored project files in a worktree** (`copyFiles`): a fresh worktree has no `.env`, local config or
  personal agent files. loopbee copies the listed paths from the developer's checkout (refusing symbolic links,
  paths Git does not ignore — they would be committed — and more than 200 MB in total), plus `CLAUDE.local.md`,
  `.claude/settings.local.json` and `AGENTS.override.md` when they exist and are ignored. loopbee's commits unstage
  every copied path, also when the agent edits `.gitignore`. Dependencies are installed by a `run` step instead.
- **The user's shell variables.** `env` values `env:NAME` are resolved from loopbee's environment, else the login
  shell (`$SHELL -ilc`, only for plain identifiers), else on Windows the saved user and system variables, because an
  app started from the Dock, the tray or at login does not inherit what the shell profile exports.

- **The agent's own MCP tools** (`agentMcp`, see below), when the Loop allows them.

When a run still cannot get something, the result contract asks the agent to list it in `missing`; the run page
shows the list with the ways to give a Loop access, and notifications include it. loopbee adds to that list what
the CLI refused for want of an approval (an MCP tool call) and a browser the macOS sandbox stopped
(`bootstrap_check_in … Permission denied`), also when the agent's own report does not mention them.

## The agent's own MCP tools

People reach a browser (a web console, an e-commerce back office, an end-to-end test), a database or a ticket
system through their agent's MCP tools. loopbee brings no browser or tools of its own: the agent uses what it is set
up with. What breaks is the approval: in their own session a person answers the CLI's prompt, in an unattended Run
nobody can. `codex exec` always runs with `approval_policy=never`, which refuses every MCP call that needs approval
(“MCP tool call requires approval, but approval policy is never”; `-c approval_policy=on-request` does not change
that). Claude Code's `-p` denies tools without an allow rule, and OpenCode's `run`
rejects them.

One switch per Loop, `agentMcp` (“Use the agent's own MCP tools”), approves every MCP tool of the agent's own setup
in advance, at any level. These tools run outside the agent's sandbox with the user's rights, so the editor asks for
a gate when they can change something and warns on Loops that work on other people's issues. Without the switch,
loopbee disables them below agent-config, so read-only stays read-only; at agent-config the agent's own settings
decide, as for everything else there.

| CLI | allowed (`agentMcp`) | not allowed (below agent-config) |
|---|---|---|
| Codex | for each server `codex mcp list --json` reports enabled in the run's folder: `-c mcp_servers.<s>.default_tools_approval_mode="approve"` when it is defined in `~/.codex/config.toml`, or `-c plugins.<plugin@marketplace>.mcp_servers.<s>…` when an installed plugin provides it (`plugins/cache/<marketplace>/<plugin>/<version>/`: `.codex-plugin/plugin.json` `mcpServers` or `.mcp.json`) | the same keys with `enabled=false` |
| Claude Code | `--settings` `permissions.allow: ["mcp__*"]`, `enableAllProjectMcpServers: true`; in a worktree `--mcp-config` with the project's local-scope servers, which `~/.claude.json` keeps under the checkout's path | `permissions.deny: ["mcp__*"]` |
| OpenCode | `OPENCODE_CONFIG_CONTENT` `"<server>_*": "allow"` for each server in `opencode.json(c)` | read-only: `"*_*": "deny"` (MCP tool names are `<server>_<tool>`) |
| pi | no approvals in pi; in a worktree of a project trusted in pi (`~/.pi/agent/trust.json`), `--approve`, so `.pi/mcp.json` loads | `--no-mcp`, or `--exclude-tools mcp__*,list_mcp_resources,…` |

Codex is asked which servers are on (`codex mcp list --json`, at most 20 seconds) and only enabled ones are named: on
the real Codex 0.162.1 a policy entry for a plugin server the user had switched off switched it back on. Only servers
defined in `~/.codex/config.toml` (tables or inline) or by a plugin can be named: a `-c` key for a server defined in
another layer, such as a project's `.codex/config.toml`, is an incomplete server entry in the layers Codex loads
first, and the real Codex refused to start (“invalid transport in mcp_servers.nexscope”). Such servers keep their own
policy; when one is still refused with the switch on, the missing list says to set `default_tools_approval_mode =
"approve"` where it is defined. When Codex cannot list its servers, loopbee names those in `config.toml` and, only to
disable them, plugin servers. Codex splits `-c` keys at dots, so a server or plugin name containing a dot is skipped. Settings › Agents lists the servers
found in each agent's user configuration, the editor shows them under the switch, and the chat designer is told
their names and when to set `agentMcp`.

A browser started from a shell command (Playwright from a script, a CLI that launches Chromium) cannot start inside
the macOS sandboxes of Codex and Claude Code: Chromium must register a Mach service, which the sandbox forbids. The
agent's browser MCP server is started by the CLI outside that sandbox and works at every level with the switch on;
a shell-started browser needs agent-config.

## Git work in worktrees

Codex's workspace-write sandbox makes Git metadata read-only, including a worktree's own gitdir: on the real CLI (0.162.1, macOS) the agent could create a branch ref but failed on `HEAD.lock` and could not switch or commit. Claude Code's sandbox allowed it. So for both CLIs loopbee does the Git work itself: with `branch` set it creates the branch in the fresh worktree, tells the agent not to branch or commit, and commits all changes after each agent step as `<Loop name>: <result summary>`. The Run records `branch` and `commits`; notifications include them.

Agents get **no** write access to Git metadata (no `--add-dir` for the common Git directory). loopbee's own Git commands in a worktree run without hooks (`core.hooksPath` → an empty folder, `commit --no-verify`) and without fsmonitor, and stop if the worktree's `.git` file changed — otherwise an agent could plant a hook (e.g. in `.husky/`) that loopbee would execute as the user. Each commit is stored as `round-NN-step-NN.diff` for the reviewer and scanned for credentials (private keys, `sk-`, `ghp_`/`github_pat_`, `AKIA`, Slack tokens); a match fails the Run. A command step that would run a file the agent changed in this Run is refused. Pushing and merging stay with the agent (workspace-network, if the user asks) or with a person after the gate.

## Command output for agent steps

A failed verification's output (last 6 KB) is added to the next round's agent prompt, so a retry is informed. A command step exiting 78 ends the Run as “nothing to do” before any agent call. See [issue-driven iteration](../workflows/issue-loop.md) for a Loop built on both.

The output of `run` and `verify` steps (last 6 KB each, with the exit status) is appended to the prompt of later agent steps in the same round. A `run` step with `continueOnError` records a non-zero exit as a failed step and continues, so an agent can report on failing tests or log queries. This matters most for Claude Code at read-only, which has no shell at all: the chat designer is told so and puts commands in a `run` step before the agent step (verified with the real CLI).

## Result contract

loopbee appends: end the final message with `LOOPBEE_RESULT {"status":"done|no_action|blocked|needs_approval","summary":"…","links":["…"]}`. The final message (Codex `--output-last-message`, else the last `agent_message`; Claude's `result` event) is stored as `round-NN-step-NN.report.md`, shown in Run details and optionally sent with notifications. `no_action` ends the Run as “Nothing to do” and skips the remaining steps (including gates); `blocked` ends it as blocked with the agent's summary; `done` and `needs_approval` continue. Without a result line the step counts as done when the CLI exits successfully. Failed turns (`turn.failed`, Claude `is_error`) fail the Run with the CLI's own error text.

Event types: Codex `thread.*`, `turn.*`, `item.*`, `error`; Claude `system`, `assistant`, `user`, `stream_event`, `result`. Everything else is stored as `agent.raw`.

## Model and token usage

After every agent step loopbee reads the model and tokens the CLI reported and stores them on the step
(`trace[].usage`) and summed on the Run (`usage`): `input` (prompt tokens not read from the cache), `cacheRead`,
`cacheWrite`, `output` (including reasoning; `reasoning` repeats that part when reported) and `costUsd` when the CLI
reports a cost. loopbee never estimates prices.

| CLI | Where it comes from |
|---|---|
| Codex | `turn.completed.usage` (`input_tokens` includes `cached_input_tokens`); the model is not in the events, so the Loop's model or `model =` in `~/.codex/config.toml` |
| Claude Code | `result.usage`, `result.total_cost_usd`, `result.modelUsage`; model also in the `system` init event |
| OpenCode | every `step_finish.part.tokens` (`input`, `output`, `reasoning`, `cache.read/write`) and `.cost`; the model is not in the events (the Loop's model, if set) |
| pi | every assistant `message_end.message.usage` (`input`, `output`, `cacheRead`, `cacheWrite`, `cost.total`) and `provider/model` |

The run page shows the total and each step's usage; the Loop page sums the last 7 and 30 days by model; the Today
board shows today's tokens and reported cost by agent and model.

## Adding an agent CLI

1. Read its headless documentation and source: how it takes a prompt on stdin, its JSON event stream, how it reports
   errors and usage, and what it can actually enforce (sandbox, tool rules, nothing).
2. Add `internal/core/agent_<name>.go` that registers an adapter with only the levels it enforces, a fake in
   `TestFakeAgent` and `tests/fake-agent/`, and cases in `TestAdapterPermissionFlags`.
3. `TestEveryAdapterMeetsTheContract` must pass unchanged; then run the real CLI once per level (`scripts/real-smoke.sh`).

## Chat authoring

`POST /api/author` runs the chosen agent once, **read-only**, inside the target project directory (or an empty scratch directory) with the conversation, the current draft and a JSON schema (`--output-schema` for Codex, `--json-schema` for Claude Code). The answer is normalised: identity, directory, agent, enabled state and secret values stay under loopbee's control, a proposal never enables a schedule, and `Validate` plus the device ceiling are checked before the user can save. The designer is told which levels its agent enforces; a proposed level the agent cannot enforce is lowered to the highest one below it that the agent supports (never raised, never to agent-config), and the reply says so. Each message is a real CLI call (typically 10–60 s, 4 min limit) on the user's own subscription.

## beectl and the skill

`beectl` talks to the running app over the loopback API with the launch token. `beectl skill install` writes `SKILL.md` to `~/.claude/skills/loopbee`, `~/.codex/skills/loopbee` and `~/.agents/skills/loopbee`, so an agent can create **disabled, private** drafts with `beectl create -f -`. Secrets in definitions are refused; drafts must be reviewed, run once and enabled in the app.

For scripts and CI: `beectl run NAME -q` prints only the run ID; `beectl cancel RUN [--wait]` takes a full ID or a
unique prefix of at least four characters (what `beectl runs` shows); `beectl cancel --loop NAME` cancels the loop's
active run (a Loop has at most one); `beectl runs --status S[,S]` filters; `beectl delete NAME --yes` deletes a
definition (runs stay; loopbee also removes the Loop's worktree and keychain entries, never branches). A name shared
by several loops is refused with their IDs instead of picking one. The skill tells agents to cancel or delete only
what the user asked for.

## Real smoke test

`tests/real-smoke.mjs` (or `sh scripts/real-smoke.sh` on a Mac, which also runs `go test` and packages the app) drives the packaged binary with the real CLIs against a throw-away Git repository with a bare origin. Per agent: **report** (chat → read-only Loop → report naming the TODO and the failing test) and **fix** (chat → worktree Loop at workspace-write → branch with a committed fix → stops at the gate → developer checkout untouched, nothing pushed → approve → done). It uses the user's subscription for a few short runs.

Results so far (Linux container, Claude Code 2.1.296): chat authoring 10–30 s with schema output; **report** passes end to end. **fix** fails there only because bubblewrap is missing, with the CLI's own error surfaced in the Run. The first real run found two defects that the fakes could not: drafts defaulted to UTC (the chat now sends the browser time zone) and Claude Code at read-only could not run `npm test` (now a `run` step with `continueOnError`).

Results on macOS 27.0 arm64 (packaged `loopbee.app`, Codex CLI 0.162.1, Claude Code 2.1.296, 2026-10-11): `go test ./...` and packaging pass; **Codex report**, **Claude report** and **Claude fix** pass end to end (branch `loopbee/fix-tests` with the committed fix, checkout untouched, nothing pushed, gate approval completes the Run). **Codex fix** was not run in the first pass: the Codex designer chose workspace-network, above the test's device limit. The designer is now told the device limit and that loopbee fetches `baseRef` itself, and the chat explains how to raise the limit when a draft exceeds it. In the second pass Codex chose workspace-write correctly but was blocked by its Git sandbox (see “Git work in worktrees”); loopbee-managed branches were added in response.

Results on macOS (packaged `loopbee.app`, pi 1.1.0, 2026-10-13): the pi **report** turn parses end to end — final message, `LOOPBEE_RESULT`, model (`provider/model`), tokens and the CLI's own cost (trimmed output kept in `internal/core/testdata/pi-real.jsonl`, checked by `TestRealPiOutput`). The pi designer proposed workspace-write, which pi cannot enforce, so the draft could not be saved; the designer is now told its levels and loopbee lowers the level. OpenCode 1.18.30 from Homebrew was killed at start (exit 137) on that Mac, which Settings showed only as “Not detected”; detection now reports the problem. Gemini CLI was not installed, so Gemini and OpenCode are still verified only against the fakes.

A second macOS run the same day (packaged app, Codex 0.162.1, Claude Code 2.1.296, OpenCode 2.0.20, pi 1.1.0): **report**
passes with Codex, OpenCode (the model is not in OpenCode's events, so usage shows tokens without a model) and pi;
the Codex **fix** draft carried a metric name without a path and could not be saved (half metrics are now dropped);
Claude Code had reached its session limit, which the chat reported verbatim.

Subscription limits surface as readable errors: Claude Code reports `is_error` with `api_error: usage_limit_reached` and a text such as “You've hit your session limit · resets 10:20pm”, which loopbee now shows verbatim in the chat and in failed Runs.

## Not verified on real CLIs

The MCP switch of 2026-10-14 was built from the CLIs' source (Codex `mcp_tool_call.rs` and `config/src/overrides.rs`,
Claude Code 2.1.299's bundled permission matcher, OpenCode's permission docs, pi-mono
`mcp-servers.ts` and `trust-manager.ts`) and is covered by fakes only. On 2026-10-14 the real Codex 0.162.1
accepted the `-c` keys for user and plugin servers (`codex mcp list` with them: approve and disable both took effect;
it also showed that an approval key re-enables a disabled plugin server and that a key for a server from another
layer stops Codex, both now avoided). Check with the real CLIs that Claude Code takes `mcp__*` and `enableAllProjectMcpServers`
from `--settings`, that OpenCode 1.x matches `"*_*"` against MCP tools, and that pi 1.1's `--tools` keeps MCP tools.
Codex issue #10390 reports that on macOS `sandbox_workspace_write.network_access=true` is ignored; check whether
workspace-network has network with Codex there.

OpenCode and pi (added 2026-10-13) were implemented from their current source and documentation (opencode and
pi-mono main branches of 2026-10-12/13) and are covered by fakes only. Before relying on them, run each real CLI once
per level: OpenCode's `OPENCODE_CONFIG_CONTENT` precedence and the older `opencode-ai` event format; pi's
`--tools`/`--exclude-tools` and failure reporting.

The agent-context changes of 2026-10-13 (read rules in `--settings`, the `Skill` tool at read-only, the memory
hint in worktrees) are covered by fake CLIs only. Check with the real CLIs before release: that Claude Code accepts
`Read(//…/**)` rules from `--settings` and the `Skill` tool name in `--tools`, and whether Codex's Windows sandbox
can read paths outside the workspace (Issue 2 reported EPERM there; loopbee passes no read flags to Codex).

The adapters are covered by fake CLIs in Go and browser tests and by the real smoke test above. Codex flags follow the official CLI reference but were not executed here. Before relying on them, run one Loop per permission level on each machine type and keep the captured output as fixtures. Whether Codex reads skills from `~/.codex/skills` or `~/.agents/skills` depends on the installed version; `beectl skill install` writes both.
