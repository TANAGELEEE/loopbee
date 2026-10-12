# Development standards

Requirements take precedence in this order: explicit user instructions → overview → project implementation contracts → static prototype examples. Missing original subpages were supplied during implementation; they are not claimed to originate from Magpie, and no Magpie code was copied.

Follow “Lessons → Standards → Tests”: when fixing a defect, update the corresponding behavior in LESSONS.md and add the smallest test that catches the original defect. Nontrivial logic needs tests. UI E2E uses the real API and temporary SQLite data; screenshots or compilation alone are insufficient.

Keep code in three layers: Go core, HTTP, and vanilla UI. Do not add a plugin host, cloud service, account system, or frontend framework. Go dependency versions and checksums are pinned; Playwright is the only npm test dependency.

Before submitting, run `go test ./...`, `go vet ./...`, `npm test`, build the binary, and run `npm run test:e2e`. For concurrency changes, also run `go test -race ./...` on a supported platform. Mac shell changes must build on macOS and receive manual checks for the tray, window closing, notifications, keychain, start at login, and keep awake. Headless cross-compilation is not a substitute.

The Windows desktop app is part of the current v1 scope; see the [platform plan](subsystems/gui-shell.md) for development order and acceptance criteria. Windows shell changes likewise require a Windows build and native window, tray, notification, credential-store, start-at-login, and keep-awake checks, alongside nogui regression tests. Linux currently requires nogui compilation checks only, without desktop acceptance requirements.

Text files are LF everywhere (`.gitattributes`); code that parses embedded or checked-in text must still tolerate CRLF, and tests must report read errors separately from content mismatches. CI runs every platform to the end (`fail-fast: false`) and uploads only evidence produced by the current run.

Do not commit launch URLs, runtime databases, secrets, app working directories, binaries, or toolchains. Do not send real notifications, run paid agents, or push to real repositories unless the current acceptance task explicitly specifies the target and authorizes it.
