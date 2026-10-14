# Updates

Loopbee updates itself from its GitHub releases, modelled on magpie's updater
([yetone/magpie](https://github.com/yetone/magpie), `internal/update` and `internal/gui/update*.go`, MIT). Source:
[`internal/update`](../../internal/update/update.go) (feed, checksums, staging, swapping, relaunch) and
[`internal/core/update.go`](../../internal/core/update.go) (state, schedule, restart).

## What happens

1. **Check.** Shortly after start (10 s) and every six hours, unless Settings › General › Updates › “Update
   automatically” is off, Loopbee reads `https://api.github.com/repos/<Repo>/releases/latest`. `Repo` is stamped
   into release builds from `$GITHUB_REPOSITORY` (`LOOPBEE_REPO` in the build scripts); `LOOPBEE_UPDATE_REPO`
   and `LOOPBEE_UPDATE_FEED` override it. A private repository answers 404 to anonymous requests; Loopbee then
   retries with the GitHub CLI's token (`gh auth token`), which it only ever sends to `api.github.com`.
   “Check now” asks at once, also with automatic updates off.
2. **Decide.** A build from source (`0.1.0-dev`, `git describe` output, `-dirty`) never updates itself: the page
   names the newest release. Equal or older releases: up to date. A newer one is downloaded at once when this copy
   can replace itself; otherwise the page links the release and says why (`stuck`): opened from Downloads (macOS
   App Translocation), run from the disk image, a folder it may not write to (Windows), headless (`-nogui`), or
   no release build for the system (Linux).
3. **Download and check.** The file for this computer (`Loopbee-macos-<arch>.zip`, `Loopbee-windows-amd64.zip`)
   must match its line in the release's `SHA256SUMS` (written by the release workflow; GitHub's per-file digest
   only fills in). A file without a published checksum, or that does not match, is never unpacked. The archive is
   unpacked beside the installed copy (`.loopbee-update`), or in the user cache when that folder is not writable.
   - macOS: `ditto -x -k`; the result must contain `Loopbee.app` whose `Info.plist` names the release's version.
     When the installed app is signed by a team (releases are unsigned today), the new one must be signed intact
     by the same team.
   - Windows: only the top-level `loopbee.exe`, `beectl.exe` and `loopbee-nogui.exe` are extracted; paths inside
     the archive are never used.
4. **Restart.** The page then offers “Restart to update” (also as a header chip). A restart asked for while Runs are
   active waits for them to end, an hour at most, because a Run cut short is marked interrupted and never
   replayed; “Restart now” asks first and does not wait. Installing:
   - macOS: the bundle is renamed aside and the new one renamed in (put back if that fails); a folder the user may
     not write to is changed after macOS's administrator prompt. A shell waits for the old process to exit and runs
     `open` on the bundle.
   - Windows: a running `.exe` cannot be overwritten but can be renamed, so it is moved aside (`.old`, removed at
     the next start) and the new one takes its name, also when it was downloaded as `Loopbee-windows-amd64.exe`;
     `beectl.exe` and `loopbee-nogui.exe` are replaced only where installed. The new program starts with
     `LOOPBEE_REPLACES=<old pid>` and waits (30 s at most) for the old one to release the data folder's lock.
5. **Quit.** Quitting with an update downloaded installs it (never with a password prompt), so the next start is
   the new version, unless automatic updates are off.

Viewers (team dashboard role) cannot restart. State: `GET /api/state` → `update{state, current, latest, notes, url,
stuck, error, done, total, checked, canRestart, waiting, gaveUp, active}`; `POST /api/update/check`;
`POST /api/update/install {when: "" | "now" | "cancel"}`.

## Not taken from magpie

magpie downloads from its own feed with per-file hashes and mirrors, re-checks the feed before restarting, keeps
release notes per language, puts back a Windows update that Smart App Control refuses to start, and can restart by
itself when idle (“auto restart”). Loopbee keeps the parts its release format supports; a mirror setting, notes
between versions and auto restart can follow.

## Verified

Go tests cover the feed, checksums (wrong or missing: refused), a private repository through the token, staging and
the Windows swap (on Linux), the bundle swap, every state, waiting for active Runs, installing on quit and viewers;
the browser E2E covers the Settings section against a fake feed.

On the developer's Mac (2026-10-14) the repository answered anonymous requests with 404 and the GitHub CLI's token
with v0.1.1 and its seven files, each with GitHub's digest. `TestRealReleaseStagesAndInstalls`
(`LOOPBEE_REAL_UPDATE=1 go test ./internal/update -run Real`) downloaded the real `Loopbee-macos-arm64.zip`, checked it
against `SHA256SUMS`, unpacked it and swapped it in for a stand-in `Loopbee.app` in a temporary folder (16 s).

So while the repository is private, only people signed in with `gh` (with access to it) get updates; everyone else
sees the error with that hint. Not verified: the restart of the packaged app (relaunch after the old process exits)
on macOS and Windows, the administrator prompt, the Windows swap on real Windows, and Windows' file locks by
antivirus or OneDrive. The first release with this code updates only to a later one.
