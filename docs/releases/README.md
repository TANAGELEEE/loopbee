# Release notes

Every release needs a notes file here, named after its tag: `docs/releases/v0.1.1.md`. The release workflow
publishes this file as the GitHub Release text and **refuses to build or publish** when it is missing, has no
`## 下載與安裝` section, or still contains placeholders (TODO, TBD, 待補). A “Full Changelog” link to the
previous tag is appended automatically.

## Sections

Use these headings in this order and leave out the ones with nothing to say:

1. `## 新增功能` — what users can do now that they could not before.
2. `## 修正問題` — bugs users could have hit, described by symptom, not by code.
3. `## 下載與安裝` — required. The table of files for this version and how to install them.
4. `## 已知限制` — what does not work yet or needs a workaround.

The **first release** describes what the version offers as a whole (use `## 這個版本提供什麼` instead of
`## 新增功能`). **Later releases** describe what changed since the previous version; use the Full Changelog and
merged changes as the source, but write for users.

## Downloads table

| Platform | File | Use |
|---|---|---|
| Windows x64 | `Loopbee-windows-amd64.exe` | Run the desktop app directly (not an installer) |
| Windows x64 | `Loopbee-windows-amd64.zip` | Portable: desktop app, headless `loopbee-nogui.exe` and `beectl.exe` |
| macOS Apple Silicon | `Loopbee-macos-arm64.dmg` / `.zip` | Open the DMG and drag Loopbee to Applications; the ZIP holds the same app |
| macOS Intel | `Loopbee-macos-amd64.dmg` / `.zip` | Same, for Intel Macs |
| All | `SHA256SUMS` | Checksums of every file above |

Add a platform (Windows ARM64, Linux) only once it is built and verified in the release workflow.

## Publishing

1. Write `docs/releases/vX.Y.Z.md`, commit it.
2. `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. To fix the text later, edit the file on the main branch and run *Actions › Release Loopbee › Run workflow*
   with the tag and **notes only** checked. Running it without “notes only” rebuilds the tag and replaces the
   attachments as well as the text.
