# Loopbee

A desktop app for local agent workflows and scheduling. Implemented from the [Loopbee overview](Loopbee總覽.md) and [prototype](doc/loopbee-design/README.md), using Go + Wails v3 (Windows / macOS) + vanilla JavaScript, with SQLite as the source of truth. [MIT licensed](LICENSE).

## Download

Download the newest version from [Releases](https://github.com/TANAGELEEE/Loopbee/releases/latest): the `.dmg` for macOS (arm64 for Apple Silicon, amd64 for Intel; drag Loopbee to Applications) or the `.exe` / `.zip` for Windows x64. The packages are unsigned: on macOS right-click `Loopbee.app` › Open the first time; on Windows choose More info › Run anyway if SmartScreen asks. Released builds then update themselves. Each release's notes list the files and how to install them. Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## Getting started

The Windows desktop app has passed local automated tests. Extract `bin/Loopbee-windows-amd64.zip` and launch `loopbee.exe`, or use the commands below. Microsoft Edge WebView2 Runtime is required; the app provides installation guidance if it is missing. The macOS desktop app still requires testing on a physical machine; Linux supports compilation and nogui mode.

Building from source requires Go 1.25 or later. Packaged apps do not require Go or Node; Node is used for testing. Install and authenticate agents separately. Git sync requires local Git credentials.

```powershell
# Windows desktop app
.\bin\loopbee.exe

# Windows headless core with the same UI in a local browser
.\bin\loopbee-nogui.exe -nogui

# Rebuild the Windows desktop app, headless app, beectl and portable ZIP
.\scripts\build-windows.ps1
```

The terminal displays the data directory and loopback address. Open the full link in `launch-url.txt` in the data directory. The launch token is exchanged for an HttpOnly session cookie and removed from the address bar. The server accepts numeric loopback addresses only, never a LAN binding. **Do not share this file or link.** Use `-data <absolute-path>` for a separate data directory and `-listen 127.0.0.1:<port>` for a fixed port.

On macOS, install Xcode Command Line Tools, then run:

```sh
go run ./cmd/loopbee
# Or build an unsigned .app
sh scripts/build-macos.sh
```

On Windows and macOS, closing the window leaves the app running in the system tray. Click the tray icon or Open Loopbee to restore it; choose Quit to stop scheduling. A second instance using the same data directory is rejected. Start at login, Keep awake, and desktop notifications are implemented for both desktop platforms. See [platform acceptance](docs/subsystems/gui-shell.md) for the Windows native test scope. Released builds update themselves from GitHub releases (checked against the release's SHA256SUMS, installed when you restart or quit); see [updates](docs/subsystems/updates.md).

## Agents and the chat

Loopbee binds to the agent CLIs on this computer — **Codex CLI** and **Claude Code** — and picks the first available one as the default (Settings › Agents). **New Loop opens a chat**: describe the job in your own words (“every weekday at 9 use our fix-prod-errors skill, merge to test, stop before master”). The default agent reads the project read-only, drafts the Loop with your real skills and scripts, and Loopbee shows the flow chart, schedule, permission and checks next to the conversation. Answer its questions, then save: drafts are saved with the schedule off. “Change by chat” on a Loop continues the same conversation. The form editor remains available.

Agent steps use your agents' own skills, plugins, AGENTS.md / CLAUDE.md. Each step ends with a small result line, so a Run can finish as **Done**, **Nothing to do** (e.g. clean logs — remaining steps and gates are skipped) or **Blocked**; the agent's final report and links appear in Run details and, if enabled per Loop, in notifications.

Permission levels: read-only → workspace write → workspace write + network (git push, deploys, databases, APIs) → the agent's own config. The device ceiling is set in Settings › General. Use a **git worktree** workspace for Loops that branch or commit, **command** steps for deterministic scripts, and **environment variables** (secrets go to the system keychain) for credentials. Waiting at a gate no longer consumes the time limit; gates expire after 72 hours by default.

From inside Codex or Claude Code: put `beectl` on PATH, run `beectl skill install`, then ask the agent to “make this a nightly Loopbee loop”. It creates a disabled draft for you to review. See [agents](docs/subsystems/agents.md).

## Working from GitHub issues

Any Loop can take its work from GitHub issues and hand back a pull request — for your own projects and for Loopbee
itself ([examples/self-improve.json](examples/self-improve.json)). Loopbee picks one issue labelled `agent-ready`
per run (only from people with write access), the agent fixes it on `loopbee/issue-<n>` following the repository's
AGENTS.md, you review the diff, and Loopbee pushes and opens the pull request, sending the link to e.g. Telegram.
The agent works without network or Git access. See [docs/workflows/issue-loop.md](docs/workflows/issue-loop.md),
including what a malicious issue could try.

## Your first Loop

1. Open Settings → Agents → Detect agents. Codex CLI, Claude Code, Gemini CLI, OpenCode and pi are supported; each enforces only the permission levels listed there (OpenCode and pi have no sandbox, so they run read-only or with their own configuration).
2. Create a Loop with an absolute working directory, prompt, steps, and limits. Defaults are read-only, private, disabled, and Require clean Git tree.
3. Steps can be observe / act / verify / gate / summarize. A verify step's `argv` is a JSON array of strings whose first entry must be allowlisted; commands are never assembled as shell strings. Approval at a gate requires a review note.
4. Run manually, or enter a five-field Cron expression and IANA timezone, then enable the Loop. After sleep, optionally catch up on the latest missed run; the app does not replay every missed occurrence.
5. View live events, review gates, cancel runs, and download evidence on the Run page. Cancellation does not undo changes already made to the workspace.

Complete importable definitions are in [examples](examples). JSON/YAML imports open in the editor and are validated on save. Archive imports receive new IDs and default to private and disabled.

## Interface

The UI follows the design boards in `doc/loopbee-design` and the “UI & interaction” spec: a compact 13 px quiet-dashboard layout (brand · segmented navigation · icon tools), KPI strip, grouped lists, lifecycle badge plus a separate business-outcome chip, and the bee motion on the flow strip and execution topology. Pages: Today, Loops (filters, schedule toggles, more menu, templates), Loop detail (flow + topology, definition summary, history, trend, versions), editor (sectioned form or JSON, live gap checks, step list with keyboard reordering), Runs (range, KPI filters, status/Loop/source filters, grouped by day), Run detail (round timeline from step records, selected-step inspector with evidence preview, raw events), gate review, artifact library with viewer, digest (15-day strip, live preview, deliveries), settings (general, agents, notifications, team, data, about) and first-run onboarding. Toasts, menus and confirmation dialogs (typed confirmation for deletes) replace browser alerts. Shortcuts: ⌘/Ctrl 1–5, ⌘/Ctrl , , ⌘/Ctrl N, ⌘/Ctrl R, `/`, Esc, Alt+↑/↓ in the editor.

The desktop window opens at 1280×820 (minimum 960×640), is clamped to the screen work area, and reopens where it was closed.

## Capabilities and limits

- Versioned Loop snapshots, resource locks, a cross-process single-instance lock, multiple steps, human gates, round/time/stall limits, process-tree cancellation, and interrupted-state recovery after restart.
- A Today dashboard, Loop and Run details, event evidence, an artifact library, daily digests, and seven-day trends; Traditional Chinese, Simplified Chinese, and English; light/dark themes, responsive layouts, and optional bee animations.
- Independent delivery and records for desktop notifications (Windows / macOS), Feishu, Telegram, and Bark. Timeouts are marked unknown and require checking before manual retry; explicit rate limits allow up to three retries. Notification failures never rerun the business Loop.
- Notification secrets and Feishu webhooks are stored in the system credential store; SQLite stores references only. Windows Credential Manager has been tested. Use `env:VARIABLE_NAME` when no credential service is available. External channels still require live delivery acceptance testing.
- A dedicated Git branch with one snapshot per publisher, containing public definitions, Runs, events, and text evidence. Viewers cannot execute, approve, cancel, or modify Loops. Publishing is blocked if common secrets or email addresses are detected. This scan is not comprehensive DLP; only make Loops public if their contents can be shared.
- ZIP archive export/import and expired-history cleanup. Exports include only non-private Loops and completed non-private Runs, without notification credentials. To back up all private data, quit the app and copy the entire data directory; preserve keychain entries separately.

Completion requires every step to succeed and a restricted expression to pass, such as `metrics.score >= 90 && verify.passed`. Metrics come only from verification commands' JSON output. If the condition is unmet, the next round retries the whole workflow. Repeated metrics or output stop execution at the stall limit. The prototype's partial subworkflow loops, automatic rollback, path-level scope/deny_paths, and fine-grained agent tool restrictions are not implemented. See [implementation contracts and differences](docs/contracts.md).

## Validation

```sh
go test ./...
go vet ./...
go build -tags nogui -o bin/loopbee ./cmd/loopbee
npm ci
npx playwright install chromium
npm test
npm run test:e2e
npm run test:screens   # optional: screenshot matrix for visual review
sh scripts/real-smoke.sh # macOS: go test + package + real Codex/Claude Code end to end (uses your subscription)
```

For complete Windows checks, run `.\scripts\check.ps1`. This checks packaged builds, the release app's native window/tray lifecycle, and workflows in real WebView2 through `npm run test:desktop`. Tests briefly open an isolated window and use temporary data paths containing Chinese characters and spaces. Only `desktoptest` builds enable a local debugging port; that executable is excluded from release ZIPs. Browser E2E continues to use `bin/loopbee.exe -nogui`.

Set `LOOPBEE_WINDOWS_INTEGRATION=1` when running core tests to check and restore start-at-login registration, test keep-awake behavior, and exercise temporary credentials. `LOOPBEE_WINDOWS_TOAST_TEST=1` submits a local test notification. E2E does not run real agents, send external notifications, or access business repositories; `scripts/real-smoke.sh` / `npm run test:real` is the opt-in check with real agent CLIs on a throw-away repository. Screenshots are in [docs/evidence](docs/evidence); set `LOOPBEE_EVIDENCE_DIR` to write them elsewhere.

### CI and releases

`.github/workflows/ci.yml` runs on every push: Go tests on Linux (plus race), Windows and macOS with `fail-fast: false`, the Windows package with native-lifecycle and WebView2 desktop acceptance, the macOS `.app`, and the browser E2E. The Windows ZIP and the macOS ZIP are uploaded as build artifacts; screenshots are written to a fresh directory and uploaded only if the run produced them.

To publish a release, write its notes in `docs/releases/vX.Y.Z.md` ([format](docs/releases/README.md)), commit, then push the tag: `git tag v0.1.1 && git push origin v0.1.1`. `.github/workflows/release.yml` stops if the notes file is missing; otherwise it tests and builds Windows x64, macOS Apple Silicon and macOS Intel (natively, version stamped in), verifies every package and publishes:

| Platform | Files |
|---|---|
| Windows x64 | `Loopbee-windows-amd64.exe` (the desktop app, run directly) · `Loopbee-windows-amd64.zip` (desktop, headless and beectl) |
| macOS Apple Silicon | `Loopbee-macos-arm64.dmg` (drag to Applications) · `Loopbee-macos-arm64.zip` |
| macOS Intel | `Loopbee-macos-amd64.dmg` · `Loopbee-macos-amd64.zip` |
| All | `SHA256SUMS` |

The release text is the notes file plus a Full Changelog link. To correct the text of a published release, edit the notes on the main branch and run the workflow by hand with the tag and **notes only**. Tags with a suffix such as `v0.2.0-rc.1` become pre-releases. Packages are unsigned and not notarized.

`.gitattributes` keeps text files LF in every checkout (PowerShell scripts CRLF); embedded files such as `cmd/beectl/SKILL.md` must not change with `core.autocrlf`.

[Architecture](docs/architecture.md) · [Contracts](docs/contracts.md) · [API](docs/api.md) · [Development standards](docs/development.md) · [Acceptance record](docs/acceptance.md) · [Lessons](LESSONS.md)
