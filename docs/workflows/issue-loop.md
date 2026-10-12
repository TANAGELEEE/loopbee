# Working from GitHub issues

Any Loop can take its work from GitHub issues and deliver it as a pull request. Both are built into Loopbee, so a
project needs nothing but a repository (and ideally an `AGENTS.md` that tells agents how to work in it). Loopbee
picks the issue, hands it to the agent, commits the change on a branch, stops for your review and opens the pull
request; merging and releasing stay with people.

```
schedule (e.g. hourly on weekdays)
  Intake      Loopbee: oldest open issue labelled agent-ready, opened by someone with write access,
              not labelled agent-working → label it agent-working          (none → “Nothing to do”, no agent call)
  Agent step  your agent in a fresh worktree on loopbee/issue-<n>, workspace-write sandbox: no network, no Git;
              gets the issue as fenced, untrusted text; follows the repository's AGENTS.md
  Commit      Loopbee commits the change (no hooks), keeps the diff, scans it for credentials
  Gate        you read the diff in Loopbee and approve with a note, or send it back
  Delivery    Loopbee pushes the branch and opens the pull request “Fixes #n”; the link goes to your phone
then: CI runs on the pull request, a person merges, releases follow docs/releases/README.md
```

## Set up a Loop for your repository

1. On the computer that runs Loopbee: install the GitHub CLI and run `gh auth login`. **Settings › Agents** shows
   whether Loopbee found it. Loopbee uses your login for reading issues, labelling, pushing and pull requests.
2. In the repository, create the label `agent-ready` (`gh label create agent-ready`); `agent-working` is created on
   first use. Optionally add an `AGENTS.md` describing how to test and what not to touch.
3. **Settings › General › Maximum permission**: *Workspace write*.
4. New Loop → describe it, for example “Every weekday hourly, fix GitHub issues labelled agent-ready in
   /Users/me/work/app; open the pull request after my review and send it to Telegram.” Or in the editor:
   **Work & delivery** → *GitHub issues* and *After my review*; the branch becomes `loopbee/issue-{issue}`.
5. Run it once by hand with a small test issue, then enable the schedule.

The agent works in a fresh worktree from the Loop's base (for example `origin/main`), not in your checkout: commit
and push what it should build on first. Uncommitted or unpushed changes are invisible to it.

| Setting | Meaning |
|---|---|
| Issue label | Which issues are ready (default `agent-ready`) |
| Repository | `owner/name` when the folder's `origin` is not where the issues live |
| Also trust | Logins trusted in addition to people with write access |
| Pull request | *No* (keep the branch) · *After my review* (needs a gate after the agent) · *Draft, review on GitHub* (no gate; the draft is the review) |
| Target branch | Defaults to the branch of the base ref (`origin/main` → `main`) |

## Loopbee improving itself

[examples/self-improve.json](../../examples/self-improve.json) is this repository's own Loop (base `origin/main`).
From a checkout of Loopbee, with the app running, run `./bin/beectl create -f examples/self-improve.json` (after
`sh scripts/build-macos.sh`; or put `bin/beectl` on your PATH, e.g. `ln -s "$PWD/bin/beectl" ~/.local/bin/beectl`),
open the draft, run it once, enable it. Loopbee then works
through its own `agent-ready` issues with the rules in [AGENTS.md](../../AGENTS.md) and
[LESSONS.md](../../LESSONS.md); you review each change and merge it; a release (`docs/releases/<tag>.md` + tag)
ships it, and you update the app. The running Loopbee only changes its source in a separate worktree, never itself.

Pair it with [examples/lessons-review.json](../../examples/lessons-review.json), which checks every Monday that fixes
came with lessons and tests.

## Getting the pull request on Telegram

1. Create a bot with @BotFather; send it one message; read your chat ID, e.g. from
   `https://api.telegram.org/bot<token>/getUpdates`.
2. **Settings › Notifications › Telegram**: token (kept in the system keychain) and chat ID, then **Send test**.
3. In the Loop's **Notifications**, choose Telegram. You get “Needs you” when the change waits at the gate (issue,
   summary, branch) and “Done” with the pull request link after delivery.

Approving needs the Loopbee window on your computer. To review only on the phone, choose *Draft, review on GitHub*:
the draft pull request link arrives on Telegram and you review and merge on GitHub. The change then reaches GitHub
before anyone has read it (CI runs it there, not on your computer).

## Writing issues the Loop can take

Label an issue `agent-ready` when one change can resolve it: what happens, what should happen, how to reproduce it.
The agent works on one issue per Run and reports **blocked** with a question instead of guessing when an issue is
unclear, too large, or needs a product decision; answer in the issue, then remove `agent-working` to let it try again.
Whatever it changed before stopping is committed on `loopbee/issue-<n>`, and the next Run continues that branch; the
review then shows the whole branch, earlier Runs included.

## When something goes wrong

| Situation | What you see | What to do |
|---|---|---|
| No `gh`, or not signed in | Run blocked: “GitHub CLI (gh) not found” | Install, `gh auth login`, Settings › Agents › Detect again |
| Run ends “Nothing to do” | The run page says why, e.g. “Skipped: #1 not labelled agent-ready” or “#1 is labelled agent-working” | Add the label (`gh issue edit 1 --add-label agent-ready`), or remove `agent-working` to retry |
| Run stops before the agent (wrong base, branch exists, …) | Run blocked with the reason | Fix it and run again; the issue was released, nothing to relabel |
| Agent reports blocked | Run ends “Blocked” with the agent's question; its changes so far are committed on the branch | Answer in the issue, remove `agent-working`; the next Run continues the branch |
| Tests that need a local port | Skipped inside the sandbox (“needs a local port, which this sandbox does not allow”) | Nothing: CI runs them on the pull request |
| You send it back at the gate | Run ends “Sent back”; nothing is pushed | Comment on the issue; remove `agent-working` to retry |
| The change contains a credential | Run fails before the gate; nothing is pushed | Inspect and delete the branch; check what the issue asked for |
| The change touches `.github/` | Run fails at delivery; nothing is pushed | Review the branch and push it yourself if it is right |
| The issue's branch already exists | The Run continues it (or starts it again if it has no commits) | Delete `loopbee/issue-<n>` to start from scratch |
| CI fails on the pull request | Red checks on GitHub | Comment on the pull request or fix by hand |

An issue keeps `agent-working` until a person removes it or the pull request (“Fixes #n”) is merged and closes it,
so no issue is taken twice.

## Security: what a malicious issue could try, and what stops it

The agent reads text other people wrote and can edit code, so an issue is untrusted input that may try to steer it
(prompt injection). Loopbee assumes the agent can be fooled and limits what a fooled agent can do. All of these run
in Loopbee itself; nothing in the repository (which the agent can edit) decides them.

| Attempt | What stops it | Evidence |
|---|---|---|
| A stranger files or comments on an issue | Only issues opened by OWNER/MEMBER/COLLABORATOR (or listed logins) are taken; other comments never reach the agent; the text is fenced and labelled as data | `TestIssueIntakeToPullRequestAfterReview`, `TestIssueIntakeWithNothingTrustedCostsNoAgentCall` |
| Make the agent send secrets over the network | The workspace-write sandbox has no network (Codex and Claude Code) | `TestBuildAgentArgs` |
| Copy a credential into the change so it gets pushed | Loopbee scans each committed diff for keys and tokens and fails the Run before the gate | `TestHostileAgentCannotEscapeThroughLoopbee` |
| Write code that something runs as you before review | No step runs between the agent and the gate in these Loops; command steps refuse files the agent changed; the editor warns about unreviewed commands | same, `TestSelfImproveExampleIsValid` |
| Plant a Git hook or redirect `.git` | Agents get no Git access; Loopbee's Git runs without hooks or fsmonitor and stops if `.git` changed | `TestHostileAgentCannotEscapeThroughLoopbee` |
| Change CI so pushing runs it with the repository's token | Changes under `.github/` are never pushed automatically | `TestDraftPullRequestAndNoAutomaticCIChanges` |
| Slip something past review | The gate shows the full diff; nothing is pushed before approval (or only as a draft); a person merges | — |
| Burn your agent quota with many issues | One issue per Run; no ready issue costs no agent call; the schedule caps the rate | `TestIssueIntakeWithNothingTrustedCostsNoAgentCall` |

What remains:

- **Reading.** The sandbox lets the agent read files outside the repository (e.g. your home folder). It cannot send
  them anywhere, and the diff scan catches common key formats, but it is pattern matching: read the diff before
  approving, especially new files and test fixtures.
- **Your approval is the real gate.** Approving pushes a branch to GitHub (public if the repository is).
- **Trusted accounts.** A compromised collaborator account is trusted. Keep branch protection and required reviews
  on the main branch so a pull request cannot merge itself.
- **Your own steps.** If you add command steps after the agent, they run its changes on your computer. The editor
  warns; keep them for tasks whose input you write yourself.
