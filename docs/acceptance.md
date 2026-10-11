# Acceptance record

Date: 2026-10-11. Original environment: Windows amd64, an isolated in-project Go toolchain, Node 22, and Chromium. The original repository contained only the overview and static designs; implementation added application code, tests, examples, build/CI configuration, and technical documentation.

## Completed validation

Scope update (2026-10-11): the Windows desktop app was implemented and passed local automated tests on Windows 11 Pro 25H2 (build 26200), amd64, WebView2 154.0.4258.62. Linux retains compilation and nogui support. See [platform acceptance](subsystems/gui-shell.md) for platform details and unverified items. The results below record that original validation, not a claim that every platform was retested during subsequent documentation edits.

| Check | Result and scope |
|---|---|
| `go test ./...` | Passed: SQLite recovery, version conflicts, concurrent claims, cancellation, time limits, gates, permissions, Cron catch-up, metric conditions, stalling, archives, keychain environment references, simulated notifications, Git sync, and HTTP security |
| `go vet ./...` | Passed |
| `npm test` | Three-language status mapping and Unicode fidelity passed |
| Playwright E2E | Passed with real HTTP/SQLite: creation, execution, approval, editing, controlled verification, evidence downloads, three languages, themes, 390px layout, and no script errors |
| Windows desktop build and packaging | `bin/loopbee.exe`: GUI subsystem, bee icon, version, PerMonitorV2 manifest, and ZIP manifest checks passed; unsigned |
| Windows native lifecycle | Release EXE passed three consecutive runs (3.05s / 4.64s / 3.92s): window display, duplicate-instance rejection, hide-on-close, background API, and tray restoration. Quit terminated the actual Node child and persisted cancelled status and end time, verified through native messages and SQLite. WebView child processes exited on their own in all three runs |
| Windows WebView2 E2E | Passed in actual desktop WebView2: creation, execution, approval, cancellation, Node verification, actual downloads, three languages, themes, forced-termination recovery, and Chinese/space-containing paths; separate test build |
| Windows system integration | Passed: per-user start-at-login registration/removal/argument restoration, keep-awake enable/disable/exit release, Credential Manager read/write/delete, and native Toast submission |
| Windows nogui build | `bin/loopbee-nogui.exe`; browser E2E also exercised the desktop app's `-nogui` mode |
| Linux amd64 nogui build | `bin/loopbee-linux-amd64`, cross-compiled only |
| macOS arm64 nogui build | `bin/loopbee-darwin-arm64-nogui`, cross-compiled only |
| Local Codex detection | 0.160.1, read version / exec help; no real task started |
| Git sharing | Local temporary bare-repository test: public definitions/events/artifacts readable by a viewer, private Loops excluded; no real repository push |

Screenshots: [Windows WebView2](evidence/windows-desktop.png), [light dashboard](evidence/dashboard-light.png), [dark dashboard](evidence/dashboard-dark.png), [mobile layout](evidence/mobile.png). Screenshot data comes from controlled E2E, not production. No sample Runs were seeded into the real app. Historical captures may retain localized text.

## Mapping to overview milestones

| Stage | Delivered | Unverified / differences |
|---|---|---|
| M0 Shell and project | Go/Wails v3, vanilla UI, single instance, platform build entry points, tray, start at login, and keep awake | Native macOS shell needs physical-machine testing; CI configured but not remotely run |
| M1 Minimal execution | Local agent detection, JSONL event archiving, SQLite, live dashboard | No real Codex execution fixture or account/model tests |
| M2 Scheduling | Cron, timezones, locks, catch-up-once/skip, scheduler activity; Windows keep-awake enable/disable passed | Actual Windows/macOS sleep/resume and logout/login still need manual acceptance |
| M3 Steps and limits | Multiple rounds, human gates, completion expressions, numeric JSON metrics, timeouts, stalling, process-tree cancellation, local permission ceiling | Prototype partial loops, rollback, path-level limits, and external cc-nexs gates are not implemented |
| M4 Artifacts | Per-step text evidence, downloads, ZIP import/export, expiry cleanup | Arbitrary workspace-file registration/preview not implemented; private data is not exported |
| M5 Notifications and languages | Four channel adapters, independent delivery/retry states, daily digest scheduling, three languages, themes, motion; native Windows Toast submitted successfully | No real external delivery; signed Feishu webhooks unsupported |
| M6 Team sync | Dedicated Git branch/checkout, per-publisher snapshots, sensitive-content detection, backend viewer read-only enforcement, shared evidence | No real user remote repository connected; scanning is conservative pattern matching, not complete DLP |
| M7 Acceptance and delivery | Automated tests, runnable Windows binaries, two-platform nogui cross-builds, unsigned Mac packaging script | Native macOS cannot be accepted from this environment; Windows race tests not run due to missing C toolchain, Linux race configured in CI |
| M8 Experimental | Claude version display, read-only scheduled-tasks inspection | Claude execution explicitly blocked; unverified source formats are not mapped automatically |

**The core and Windows desktop app passed local automated testing, but this does not establish complete prototype or all-platform v1 acceptance.** See contracts.md for prototype differences. Real agents, native Mac features, actual sleep/resume and login, and external notifications require separate integration acceptance.

## Second pass: UI rebuild and window sizing (2026-10-11)

Environment: Linux amd64 container, Go 1.25.0 built from source, Chromium 1194 (Playwright). The container cannot reach `proxy.golang.org` or GitLab, so modules were resolved from their GitHub mirrors and `modernc.org/sqlite` was replaced *for local testing only* with a shim over `mattn/go-sqlite3`; `go.mod`/`go.sum` are unchanged and release builds still use modernc.

| Check | Result |
|---|---|
| `go test ./...` (incl. new `TestRunTraceRecordsRoundsGatesAndArtifacts`, `TestRerunAcknowledgeAndPause`, `TestFitWindow`) | Passed on Linux |
| `GOOS=windows go vet ./cmd/... ./internal/...` | Passed (compiles the Wails desktop shell for Windows) |
| `npm test` (key coverage in three languages, placeholders, no hard-coded CJK in views) | Passed |
| `npm run test:e2e` | Passed: onboarding, editor, run, gate note enforcement, edit, evidence preview, artifact download, cancel dialog focus, bee/live-link counts and motion-off, three languages, themes, 960 px and 390 px without horizontal overflow, session cookie required |
| `npm run test:screens` | 1280×820 matrix (zh-TW/en/zh-CN, light/dark) reviewed against the design PNGs; no overflow, no console errors |

Not run in this pass: Windows native lifecycle and WebView2 desktop E2E (`scripts/check.ps1`), macOS build. `tests/desktop-e2e.mjs` was updated to the new UI and only syntax-checked. Run `scripts/check.ps1` on Windows before release.

## Third pass: agent binding, chat authoring and beectl (2026-10-11)

Same container and module setup as the second pass.

| Check | Result |
|---|---|
| `go test ./...` incl. TestBuildAgentArgs, TestAgentStepOutcomesForBothCLIs (fake Codex and Claude Code × done/no_action/blocked/plain/fail), TestGateWaitIsOutsideTheTimeBudgetAndExpires, TestCommandStepEnvironmentAndFailure, TestWorktreeWorkspaceLeavesTheCheckoutAlone (real git), TestAuthorProposalIsNormalisedDraft, TestRunNotificationContentAndRules, TestLoginShellPath, beectl tests | Passed |
| `GOOS=windows go vet ./cmd/... ./internal/...` | Passed |
| `npm test` (i18n coverage incl. chat.js) | Passed |
| `npm run test:e2e` = e2e.mjs + agents-e2e.mjs (fake CLIs via LOOPBEE_*_BIN) | Passed: default agent switch, chat with follow-up question, preview, save as disabled draft with conversation, Claude-run agent step with report/links and gate, no_action skips the gate, form shows permission levels, worktree base and secret gaps |
| Real Claude Code 2.1.296 | Accepted the workspace-network flag set and refused to run without bubblewrap/socat, as intended |

Not run: real Codex CLI, real model calls, Windows/macOS desktop builds of this pass, real notification channels.
