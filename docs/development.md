# Development standards

Requirements take precedence in this order: explicit user instructions → overview → project implementation contracts → static prototype examples. Missing original subpages were supplied during implementation; they are not claimed to originate from Magpie, and no Magpie code was copied.

## Lessons → Standards → Tests

Every defect leaves three traces: a **test** that fails if it comes back, a **lesson** in [LESSONS.md](../LESSONS.md)
(rule, reason, evidence, the days it was seen), and, once it has recurred on three separate days, a **standard** in
the section below. `npm test` (tests/lessons.test.mjs) fails when a lesson's evidence does not exist or when a lesson
seen on three days has not been promoted. Agents read the same files: Codex through [AGENTS.md](../AGENTS.md),
Claude Code through [CLAUDE.md](../CLAUDE.md).

When the loop runs:

| Moment | What to do |
|---|---|
| Fixing a defect | Failing test → fix every place with the same assumption → add the lesson or a new Seen date |
| Pull request | List the lessons added or updated; the PR template asks for them |
| Weekly review | The read-only Loop [examples/lessons-review.json](../examples/lessons-review.json) compares the week's commits with LESSONS.md and reports fixes without a lesson or test |
| Release | User-visible fixes since the last tag become “修正問題” in `docs/releases/<tag>.md` |
| Third sighting | Move the rule into “Standards” below; the lesson moves to “Moved to the standards” |

Nontrivial logic needs tests. UI E2E uses the real API and temporary SQLite data; screenshots or compilation alone are
insufficient.

Keep code in three layers: Go core, HTTP, and vanilla UI. Do not add a plugin host, cloud service, account system, or frontend framework. Go dependency versions and checksums are pinned; Playwright is the only npm test dependency.

Before submitting, run `go test ./...`, `go vet ./...`, `npm test`, build the binary, and run `npm run test:e2e`. For concurrency changes, also run `go test -race ./...` on a supported platform. Mac shell changes must build on macOS and receive manual checks for the tray, window closing, notifications, keychain, start at login, and keep awake. Headless cross-compilation is not a substitute.

The Windows desktop app is part of the current v1 scope; see the [platform plan](subsystems/gui-shell.md) for development order and acceptance criteria. Windows shell changes likewise require a Windows build and native window, tray, notification, credential-store, start-at-login, and keep-awake checks, alongside nogui regression tests. Linux currently requires nogui compilation checks only, without desktop acceptance requirements.

Text files are LF everywhere (`.gitattributes`); code that parses embedded or checked-in text must still tolerate CRLF, and tests must report read errors separately from content mismatches. CI runs every platform to the end (`fail-fast: false`) and uploads only evidence produced by the current run.

Do not commit launch URLs, runtime databases, secrets, app working directories, binaries, or toolchains. Do not send real notifications, run paid agents, or push to real repositories unless the current acceptance task explicitly specifies the target and authorizes it.

## Standards

Rules that have proven themselves; promoted lessons are added here with a link back to their history.

- **Real agent CLIs before release.** Changes to agent adapters, the authoring prompt, workspaces or result parsing are
  verified with `scripts/real-smoke.sh` against the real Codex CLI and Claude Code before they are released; fake CLIs
  cover only loopbee's side (decided 2026-10-12 after four real-only defects, see LESSONS “Real agent CLIs”).
- **Report what was not verified.** Every hand-off lists the checks that were not run (platform builds, real CLIs,
  CI-only steps) instead of implying them.
- **One source for every list.** A list that more than one place needs (step kinds, agent CLIs, permission levels)
  has one definition the others read: the Go core owns it and the UI reads it from State. Adding an item is one change
  plus its test (promoted 2026-10-13 after three sightings, see LESSONS “Moved to the standards”).
- **Text is LF.** `.gitattributes` keeps text LF in every checkout; code that parses checked-in or embedded text still
  tolerates CRLF.
