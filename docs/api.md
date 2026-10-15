# JSON HTTP API

The server listens only on numeric loopback addresses. Every `/api/*` route except session requires the `loopbee` cookie. Writes require `Content-Type: application/json`, except archive uploads, which use `application/zip`. Successful writes return the complete new State. Errors return HTTP 400 with `{ "error": "..." }`; authentication/origin failures return 401/403. Error messages are currently English, and status codes are independent of display language.

| Method | Path | Input / response |
|---|---|---|
| POST | `/api/session` | `{token}` → HttpOnly cookie |
| GET | `/api/state` | brand, settings (secrets masked), loops, runs, agents, deliveries, activity, updated, team `{last, error, pending}` (the last team sync) |
| GET | `/api/digest` | `{text}` for the configured digest language/timezone |
| POST | `/api/loops` | Complete Loop; empty id creates one, updates require version |
| DELETE | `/api/loops/{id}` | Delete the definition and retain history; rejected while a Run is active. Also removes the Loop's worktree and the secret values it stored in the system keychain; branches stay |
| POST | `/api/loops/{id}/run` | `{}`; acquire resource locks and save a snapshot |
| POST | `/api/runs/{id}/cancel` | `{}` |
| POST | `/api/runs/{id}/gate` | `{approve: boolean, note: string}` |
| GET | `/api/runs/{id}/events` | Latest 1,000 Events, newest first |
| GET | `/api/artifacts/{id}/{name}` | Registered text evidence attachment |
| POST | `/api/settings` | Complete Settings; `********` preserves an existing secret |
| POST | `/api/agents/detect` | `{}`; detect versions and capabilities |
| POST | `/api/notify/{kind}/test` | `{}`; independently test an enabled channel |
| POST | `/api/deliveries/{id}/retry` | `{}`; only failed/unknown deliveries, creates a new delivery |
| POST | `/api/sync` | `{}`; executors publish, viewers pull. Both also happen by themselves once a team repository is set: an executor publishes when it is set and about 30 s after a Run ends or waits at a gate or a Loop changes; a viewer pulls when it is set, at start and every 5 minutes |
| POST | `/api/prune` | `{}`; remove expired terminal Runs |
| GET | `/api/export` | ZIP after sensitive-content checks |
| POST | `/api/archive/import` | ZIP bytes, maximum 32 MiB |
| POST | `/api/definition/parse` | `{source}` JSON/YAML → unsaved, private, disabled Loop |
| GET | `/api/import/claude` | Read-only candidate source text from the user's scheduled-tasks directory |
| POST | `/api/runs/{id}/rerun` | `{}`; start a new Run of the same Loop (`source: "rerun"`, `rerunOf`); the old Run is untouched |
| POST | `/api/runs/{id}/ack` | `{}`; mark an interrupted Run as reviewed (`acked`); never restarts anything |
| POST | `/api/scheduler/pause` | `{paused}`; pause/resume all future triggers. Active Runs continue; paused triggers are not recorded as missed |
| POST | `/api/onboarding/done` | `{}`; first-run setup finished or skipped (`settings.onboarded`) |
| GET | `/api/diagnostics` | Version, platform, data directory, agent detection and counts; no Loop content or secrets |
| GET | `/api/artifacts/{id}/{name}?inline=1` | Same evidence, served inline for the in-app viewer |
| POST | `/api/author` | `{agent, directory, messages:[{role,text,at}], draft?}` → `{reply, questions:[{text,options}], loop, problem, agent}`. Runs the agent CLI read-only in `directory`; `loop` is an unsaved, disabled draft |

State additions: `info` (version, os, arch, dataDir, active runs); each Run carries `trace` (one record per executed step: round, step, kind, name, status `running|waiting|ok|failed|approved|rejected|cancelled|interrupted`, started/finished, artifact, bytes, note — gate notes and metric values land in `note`), `source` (`manual|schedule|catchup|rerun`), `rerunOf` and `acked`. Settings gain `onboarded`, `paused` and `defaultAgent`; `maxSandbox` accepts `read-only|workspace-write|workspace-network|agent-config`. Loops gain `workspace`, `baseRef`, `branch` (worktree only; `{date}`, `{time}`, `{run}`, `{issue}` placeholders), `intake{kind,repo,label,workingLabel,trustedUsers}`, `pullRequest{mode,base,merge}`, `gateHours`, `env[{name,value,secret}]` (`value` may be `env:NAME`, resolved from loopbee's environment, else the login shell or, on Windows, the saved user variables), `readPaths` (read-only folders/files outside the project; `~/` = home), `copyFiles` (worktree only: Git-ignored project files copied into each worktree, never committed), `notify{channels,events,includeReport}`, `conversation` and `source`; step kind `run` with optional `continueOnError`. Runs gain `report`, `result{status,summary,links,missing}` (`missing`: what the agent could not get), `workdir`, `branch`, `commits`, `issue{number,title,url,author}`, `prUrl`, `merged` and `phase` (`delivering` while the branch is pushed and the pull request opened, `merging` while its checks run); state gains `github{version,signedIn}`; `POST /api/author` accepts `timezone`; status `no_action`. Agents carry `caps`, `label`, `levels` (the permission levels the CLI enforces), `enforcement` and `flags` per level. State gains `update` (loopbee's own updates, see [updates](subsystems/updates.md)) and Settings `noAutoUpdate`; `POST /api/update/check` asks for the newest release, `POST /api/update/install {when}` restarts into a downloaded one (`""` waits for active Runs, `"now"`, `"cancel"`). Loops gain `model` and `agentMcp` (approve the agent's own MCP tools in advance; off disables them below agent-config); Agents gain `problem`, `mcp` (servers found in the agent's user configuration) and `mcpFlags{on,off}`; Runs and each `trace[]` record gain `usage{agent,model,input,cacheRead,cacheWrite,output,reasoning,costUsd}`. `beectl` (cmd/beectl) uses these routes with the launch token.

The UI reads State every three seconds. Rerendering pauses while the editor, a dialog, a menu or a focused form field is active to avoid losing input. Settings refresh explicitly, and the editor retains drafts. Interface, agent report, and digest languages are stored independently and do not overwrite one another.
