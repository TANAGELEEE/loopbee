# Lessons

Read this before changing code. Every entry is something a change in this repository once got wrong, written so
the next change does not repeat it. Agents (Codex, Claude Code, Loopbee's own Loops) and people use the same file.

Each lesson is a rule (the heading), the reason, and two lines that `tests/lessons.test.mjs` checks in `npm test`:

- **Evidence** — what fails if the mistake comes back: a Go test name (`TestName`), a test file (`tests/x.mjs`), or
  `path#text` for a check that lives in configuration (the file must contain the text). Every item must exist.
- **Seen** — the days the problem was found (`YYYY-MM-DD`), or `initial build` for lessons from the first version.

A lesson seen on **three separate days** is a pattern, not an accident: move its rule into
[docs/development.md](docs/development.md) and list it under [Moved to the standards](#moved-to-the-standards).
The check fails while such a lesson is still here.

## How to add a lesson

1. Reproduce the defect with the smallest failing test (Go test, browser E2E, or a config check).
2. Fix the whole class: search for every other place with the same assumption, not just the one you saw.
3. Add the lesson under the matching theme, or add today's date to **Seen** if it already exists.
4. Mention new or updated lessons in the PR and, for user-visible fixes, in `docs/releases/<next tag>.md`.

## Fix the class, not the sample

### Local CLI information must not require the app to be running
`beectl version` reached the app connection and `--version` failed flag parsing. Handle both before connecting,
print the CLI's own build version, and list them in help.
- Evidence: `TestBeectlVersionWithoutApp`, `TestBeectlHelpListsVersion`
- Seen: 2026-10-12

### When the same list exists in two places, fix both or make them share one source
The 10-11 fix added “Command” to the add-step menu, but the function that inserts the step kept its own name
table without `run`: new command steps were named `undefined` and had no command field. Both now use `kindLabel`.
- Evidence: `tests/agents-e2e.mjs#Every step kind in the add menu has a label`
- Seen: 2026-10-11, 2026-10-12

### Text that code parses must not depend on the checkout's line endings
A Windows checkout with `core.autocrlf` embedded `cmd/beectl/SKILL.md` with CRLF: the test said “skill not
written” although the file existed, and the schema example cut at "```json\n" was silently lost. Keep text LF in
every checkout and normalise in code; tests report read errors separately from content mismatches.
- Evidence: `TestSkillWithCRLFCheckout`, `.gitattributes#* text=auto eol=lf`
- Seen: 2026-10-12

## Real agent CLIs

Fake CLIs prove Loopbee's side of the contract; only the real CLIs show the other side. Every difference found with
a real CLI becomes fake behaviour plus a test, and `scripts/real-smoke.sh` runs before releasing changes to the
adapters, the authoring prompt or workspaces.

### Run the real CLIs before trusting the fakes
All fake-CLI tests passed while the first real run found four defects (the four lessons below).
- Evidence: `tests/real-smoke.mjs`, `scripts/real-smoke.sh#real-smoke.mjs`
- Seen: 2026-10-11

### Read-only Claude Code has no shell; give it command output instead
“Run the tests and report” could not run `npm test`. Command steps run first (with `continueOnError` when a failure
is information) and their output is appended to later agent prompts.
- Evidence: `TestCommandOutputReachesTheAgentStep`, `TestAuthorPromptKnowsTheAgentAndTimezone`
- Seen: 2026-10-11

### Agents do not write Git metadata; Loopbee commits for them
Codex's workspace-write sandbox makes `.git` read-only, including a worktree's gitdir: it created a branch and
failed on `HEAD.lock`. Loopbee creates the branch in the worktree and commits after each agent step; agents get no
Git access at all (see “Code the agent wrote never runs as you before review”).
- Evidence: `TestBranchWorkspaceCommitsForTheAgent`, `tests/agents-e2e.mjs#Agents cannot commit in a worktree`
- Seen: 2026-10-11

### Schedules use the user's time zone
Chat drafts defaulted to UTC, so “9:00” ran at 17:00 in Taipei. The UI sends the browser's time zone.
- Evidence: `TestAuthorUsesTheUserTimezone`
- Seen: 2026-10-11

### The designer must know the device's permission limit
Codex asked for network access to “fetch origin/master”, which Loopbee already does, and the draft could not be
saved. The prompt states the limit and what Loopbee does itself; a draft above it says how to raise the limit.
- Evidence: `TestAuthorFlagsPermissionAboveTheDeviceLimit`, `tests/agents-e2e.mjs#Raise the limit in Settings`
- Seen: 2026-10-11

### An error says how to fix it
`beectl create` answered a too-high permission with “permission exceeds this device's limit” and nothing else; the
user could not tell what to change. It now names the level the draft needs, the device's limit and where to raise it.
- Evidence: `TestBeectlCreatesDisabledDraftAndRuns`
- Seen: 2026-10-12

### Show the CLI's own error, never its raw output
A missing sandbox and a reached subscription limit showed up as generic failures or raw JSON.
- Evidence: `TestAgentFailureReason`, `TestAuthorReportsAgentUsageLimit`, `tests/agents-e2e.mjs#session limit`
- Seen: 2026-10-11

### Network is an explicit permission level
A sandbox without network silently breaks git push, deploys and databases. Map each level to exact CLI flags and
show them.
- Evidence: `TestBuildAgentArgs`
- Seen: 2026-10-11

### Exit code 0 is not a result
“Agent exited 0” cannot tell done, nothing to do and blocked apart. Agents end with a `LOOPBEE_RESULT` line.
- Evidence: `TestAgentStepOutcomesForBothCLIs`
- Seen: 2026-10-11

### An agent's proposal is input, not a definition
A proposal could enable a schedule or carry secret values. Drafts stay disabled and private, secret values are
dropped, and validation and the device limit still apply.
- Evidence: `TestAuthorProposalIsNormalisedDraft`, `TestBeectlCreatesDisabledDraftAndRuns`
- Seen: 2026-10-11

### Reuse the team's agent workflows instead of rebuilding them as steps
Teams already have complete skills; a step-by-step editor duplicated them and blocked adoption. Default to one
agent task plus an optional gate, authored by chat; the form stays for experts.
- Evidence: `tests/agents-e2e.mjs`
- Seen: 2026-10-11

## Untrusted input

Loops that read what other people write (issues, e-mail, web pages, logs) must assume the agent can be talked into
anything its sandbox allows.

### Code the agent wrote never runs as you before review
The first issue Loop ran `go test` after the agent and before the gate, outside any sandbox, and Loopbee's commit
ran repository hooks the agent could write (`.husky/`): a prompt-injected issue could have run code with the user's
credentials. Now agents get no Git access, Loopbee's Git runs without hooks and checks `.git`, command steps refuse
files the agent changed, the editor warns about commands between an agent and a gate, and the example has none.
- Evidence: `TestHostileAgentCannotEscapeThroughLoopbee`, `TestSelfImproveExampleIsValid`, `tests/agents-e2e.mjs#runs after the agent and before any review`
- Seen: 2026-10-12

### The gate shows what will be pushed
The first real review listed only the agent's raw JSON output, cut to 14 lines; the committed diff existed but the
gate never offered it. The gate now opens on “Changes to review”, followed by the report, and shows them in full;
on a continued branch it shows everything not yet reviewed, also when this Run committed nothing new.
- Evidence: `tests/agents-e2e.mjs#Changes to review`, `TestBlockedIssueWorkIsKeptAndTheRetryContinuesIt`
- Seen: 2026-10-12

### Only trusted people's words reach the agent
Anyone can open or comment on a public issue. Loopbee's intake takes issues opened by people with write access,
drops other comments, and fences the text as data.
- Evidence: `TestIssueIntakeToPullRequestAfterReview`, `TestIssueIntakeWithNothingTrustedCostsNoAgentCall`
- Seen: 2026-10-12

### Review what is pushed, and stop credentials before review
The reviewer saw the agent's report, not its change. Each commit is kept as a diff and scanned for credentials.
- Evidence: `TestHostileAgentCannotEscapeThroughLoopbee`, `TestSecretsInPatchIgnoresOrdinaryCode`
- Seen: 2026-10-12

### Picking work and delivering it belongs to Loopbee, not to the project
The first issue Loop used scripts in this repository to pick issues and open pull requests: they only worked here,
the agent could edit them, and every other project would have needed a copy. Intake and pull requests are now Loop
settings implemented in Loopbee; the project only says how to work (AGENTS.md).
- Evidence: `TestIssueIntakeToPullRequestAfterReview`, `TestDraftPullRequestAndNoAutomaticCIChanges`, `TestSelfImproveExampleIsValid`
- Seen: 2026-10-12

### Never assume the default branch is master
The self-improvement example used `origin/master` in a repository whose default branch is `main`, and its first run
could not create the worktree. A missing base now names the remote branches that exist.
- Evidence: `TestMissingBaseRefNamesTheRemoteBranches`
- Seen: 2026-10-12

### Release a claim when no agent worked on it
Intake labels the issue `agent-working` before the worktree exists. The first self-improvement Run stopped on a
wrong base ref and kept the label, so every later Run skipped issue #1 and ended “Nothing to do”. Loopbee now
removes the label when a Run ends before an agent started; after that, the label stays until a person removes it.
- Evidence: `TestIntakeExplainsNothingToDoAndReleasesUnstartedClaims`
- Seen: 2026-10-12

### Changes to CI configuration are never pushed automatically
A pushed workflow file runs with the repository's token before anyone merges it.
- Evidence: `TestDraftPullRequestAndNoAutomaticCIChanges`
- Seen: 2026-10-12

## Runs, schedules and gates

### Claim a Run and its locks under one mutex
Retried triggers started duplicate runs.
- Evidence: `TestConcurrentStartAndCancel`
- Seen: initial build

### Saving compares versions
An old editor overwrote a newer version.
- Evidence: `TestOptimisticVersion`
- Seen: initial build

### A stopped process may already have had side effects
Mark recovered runs interrupted; never replay them.
- Evidence: `TestRecovery`
- Seen: initial build

### Waiting at a gate does not use the run's time limit
Overnight approvals timed out. Only step execution is budgeted; gates have their own expiry.
- Evidence: `TestGateWaitIsOutsideTheTimeBudgetAndExpires`
- Seen: 2026-10-11

### Record what ran; do not reconstruct it
A timeline rebuilt from free-text events was guesswork. The runner records one trace entry per executed step.
- Evidence: `TestRunTraceRecordsRoundsGatesAndArtifacts`
- Seen: 2026-10-11

### Never touch the developer's checkout
Agents switching branches collided with work in progress. Runs can use a fresh worktree from a base ref.
- Evidence: `TestWorktreeWorkspaceLeavesTheCheckoutAlone`
- Seen: 2026-10-11

### Polling Loops end quietly when there is nothing to do
An hourly issue Loop would otherwise spend an agent call per run. A command step exiting 78 ends the Run as
“nothing to do” before any agent or gate.
- Evidence: `TestCommandStepNothingToDoAndRunEnvironment`
- Seen: 2026-10-12

### A quiet end still says why
A “Nothing to do” Run showed “All steps succeeded and the completion condition was met.” and hid the reason Loopbee
had recorded. The run page shows the Run's own summary, and intake names the issues it passed over (not labelled,
already being worked on, opened by someone without write access).
- Evidence: `tests/agents-e2e.mjs#No actionable errors`, `TestIntakeExplainsNothingToDoAndReleasesUnstartedClaims`
- Seen: 2026-10-12

### An unfinished Run keeps its work
In the first self-improvement Run Codex fixed issue #1 and reported blocked; Loopbee ended the Run before
committing, the next Run would have deleted the worktree, and the leftover branch blocked every retry. Loopbee now
commits before acting on the agent's result, and a retry of the same issue continues its branch; the reviewer sees
the whole branch. On that retry Codex found the fix already there and reported “nothing to do”, which ended the Run
before review: work already on the branch now goes to review and the pull request.
- Evidence: `TestBlockedIssueWorkIsKeptAndTheRetryContinuesIt`
- Seen: 2026-10-12

### A decision is saved when it is made
After “Approve and continue” the Run stayed “waiting” in storage while Loopbee pushed and opened the pull request,
so the page kept offering the gate and the click looked ignored. The approval and the delivery phase are saved at
once, and a push never waits for a credential prompt nobody can see.
- Evidence: `TestApprovalShowsDeliveryWhileItRuns`, `TestPushNeverWaitsForCredentials`
- Seen: 2026-10-12

### A retry must see why the previous round failed
Each round started with an empty context, so after a failed verification the agent retried blind. The failed
check's output is now part of the next round's prompt.
- Evidence: `TestVerifyFailureReachesTheNextRound`
- Seen: 2026-10-12

### What the reviewer acts on must reach the notification
The pull request was opened by a command step, but notifications only carried the agent's result: the link never
reached the phone. Command steps can print a `LOOPBEE_RESULT` line; its summary and links join the Run's result.
- Evidence: `TestCommandStepResultReachesTheNotification`, `TestIssueIntakeToPullRequestAfterReview`
- Seen: 2026-10-12

### Unknown deliveries are not retried automatically
A timed-out delivery may already have arrived.
- Evidence: `TestNotificationResponses`
- Seen: initial build

### A manual retry cannot replay the same record
Mark the original retried and create the new pending delivery in one transaction.
- Evidence: `TestRetryCannotBeReplayed`
- Seen: initial build

## Security and data

### The core enforces read-only access, not the buttons
Hiding buttons did not stop a viewer. Start, SaveLoop, Gate, Cancel and Prune reject viewers.
- Evidence: `TestViewerAndArtifactTraversal`, `TestTeamSyncRoundTrip`
- Seen: initial build

### Local history and shared data stay separate
Switching roles overwrote local dashboard history; shared data lives in `shared_*`.
- Evidence: `TestTeamSyncRoundTrip`
- Seen: initial build

### Localhost HTTP still needs CSRF and DNS-rebinding protection
Bind to loopback, check Host and Origin, and require the random launch session.
- Evidence: `TestAuthenticationAndOrigin`, `TestLoopbackOnly`
- Seen: initial build

### Scan parsed text for secrets, not raw JSON
Nested JSON escapes hid secrets from flat patterns.
- Evidence: `TestSensitiveContentScanner`
- Seen: initial build

### Archive paths, sizes and IDs are untrusted
Enforce extraction limits and manifests; assign new IDs and disable imported Loops.
- Evidence: `TestArchiveTraversalRejected`, `TestArchiveRoundTrip`
- Seen: initial build

### Notification text is data, never script
XML-encode it and pass it over stdin to a fixed script.
- Evidence: `TestWindowsToastContentIsData`
- Seen: initial build

## Desktop and platforms

### Headless and browser tests do not replace the desktop
Test the release executable's native lifecycle and drive a test build through real WebView2.
- Evidence: `TestNativeDesktopLifecycle`, `tests/desktop-e2e.mjs`
- Seen: initial build

### A tray lookup can return nothing and no error
Check process ownership, errors and a non-empty rectangle.
- Evidence: `TestNativeDesktopLifecycle`
- Seen: initial build

### Windows keep-awake belongs to the calling thread
Enable and release it on the same locked OS thread; end that thread if cleanup fails.
- Evidence: `TestWindowsKeepAwakeThreadLifecycle`
- Seen: initial build

### Login commands contain spaces, Chinese characters and trailing backslashes
Use the system's quoting rules and check the registry command length.
- Evidence: `TestWindowsAutostartCommandRoundTrip`
- Seen: initial build

### Windows pipes can corrupt Chinese text
Edit UTF-8 files directly and check translations for replacement characters.
- Evidence: `tests/i18n.test.mjs`
- Seen: initial build

### The first window must fit the screen
A fixed 1280×900 window did not fit 1080p at 150 % or 1366×768. Clamp to the work area and remember bounds.
- Evidence: `TestFitWindow`
- Seen: 2026-10-11

## Interface

### Compiling is not proof that the UI works
Exercise creation, runs, approval, downloads, languages and themes in a real browser against the real API.
- Evidence: `tests/e2e.mjs`
- Seen: initial build

### Locate text by its region, not by exact container text
Status icons broke exact-text locators.
- Evidence: `tests/e2e.mjs`
- Seen: initial build

### A refresh must never discard what the person typed
In the macOS window the first click after typing in a text box is swallowed; by the time the reviewer clicked
again, the 3-second refresh had re-rendered the gate and wiped the review note, so approving seemed to do nothing.
The note is kept per Run and the page does not refresh over it.
- Evidence: `tests/agents-e2e.mjs#the review note was wiped by a refresh`
- Seen: 2026-10-12

### Do not override the design system from the app stylesheet
Copying the design CSS and overriding it changed header height, title size and buttons. `app.css` only adds
layout and states; component sizes, spacing and colours stay in `loop.css`.
- Evidence: `tests/screens.mjs`
- Seen: 2026-10-11

### The CSP forbids inline styles
Design markup like `style="--c:…"` silently did nothing. Use data attributes and apply variables through CSSOM.
- Evidence: `tests/e2e.mjs`
- Seen: 2026-10-11

### Lifecycle and outcome are two signals
One badge hid that “ended” is not “goal reached”. The badge shows the lifecycle; a chip shows the outcome.
- Evidence: `tests/screens.mjs`, `tests/e2e.mjs`
- Seen: 2026-10-11

## Verification, CI and releases

### One failing platform must not hide the others
The matrix cancelled Linux race tests when Windows failed.
- Evidence: `.github/workflows/ci.yml#fail-fast: false`
- Seen: 2026-10-12

### Upload only evidence produced by this run
CI uploaded screenshots committed in `docs/evidence` as if this run had taken them.
- Evidence: `.github/workflows/ci.yml#LOOPBEE_EVIDENCE_DIR`, `tests/desktop-e2e.mjs#LOOPBEE_EVIDENCE_DIR`
- Seen: 2026-10-12

### Tests say when the sandbox, not the code, stopped them
Inside Codex's sandbox `go test ./...` panicked on “bind: operation not permitted”, so the agent could not tell a
sandbox limit from a broken change and reported blocked. Tests that need a local port skip there with that reason
(never in CI), and AGENTS.md and the issue prompt say a sandbox limit is not a reason to stop.
- Evidence: `TestLoopbackSkipsOnlyInsideASandbox`, `AGENTS.md#cannot open local ports`, `TestIssueIntakeToPullRequestAfterReview`
- Seen: 2026-10-12

### Do not parse Git's short names
`git branch -r --format=%(refname:short)` prints a remote's HEAD pointer (`refs/remotes/origin/HEAD`) as a bare
`origin`. Newer Git records that pointer on every fetch, older Git (2.43 here) does not, so every CI platform failed
with “one of: origin, origin/master” while the tests passed locally. Read full ref names, skip HEAD pointers, and
create the pointer in the test so every Git version sees it.
- Evidence: `TestMissingBaseRefNamesTheRemoteBranches`
- Seen: 2026-10-12

### Release text is written, not generated
`--generate-notes` produced one line because it only lists merged PRs. Each tag publishes
`docs/releases/<tag>.md`, and the release stops without it.
- Evidence: `.github/workflows/release.yml#--notes-file`, `tests/lessons.test.mjs`
- Seen: 2026-10-12

## Moved to the standards

Nothing yet. Lessons move here, with the dates they were seen, after three separate days.
