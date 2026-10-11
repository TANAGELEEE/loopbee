# Lessons → Standards → Tests

| Lesson | Standard | Regression evidence |
|---|---|---|
| Hiding buttons does not enforce read-only access | Core Start / SaveLoop / Gate / Cancel / Prune must reject viewers | TestViewerAndArtifactTraversal, TestTeamSyncRoundTrip |
| Switching roles must not overwrite local dashboard history | Keep local data separate from shared_* data | TestTeamSyncRoundTrip |
| Retried triggers can start duplicate runs | Claim Runs and acquire Loop/resource locks under the same mutex | TestConcurrentStartAndCancel |
| An old editor can overwrite a newer version | Compare versions on save | TestOptimisticVersion |
| A stopped process may already have caused side effects | Mark recovered runs interrupted; do not replay them | TestRecovery |
| HTTP localhost still carries CSRF / DNS rebinding risks | Require loopback binding, Host/Origin checks, and a random launch session | TestAuthenticationAndOrigin, TestLoopbackOnly |
| A timed-out delivery may already have arrived | Never automatically retry unknown deliveries | TestNotificationResponses |
| Nested JSON escapes can hide secrets from flat regexes | Scan parsed nested text | TestSensitiveContentScanner |
| ZIP paths, sizes, and IDs cannot be trusted | Enforce extraction limits and manifests; assign new IDs and disable imports | TestArchiveTraversalRejected, TestArchiveRoundTrip |
| Text locators must account for status icons | Assert that the whole status region contains the label, not that its container has only plain text | tests/e2e.mjs |
| Compilation does not prove UI behavior | Exercise creation, execution, approval, downloads, languages, and themes in a real browser | tests/e2e.mjs |
| Windows pipe encodings can corrupt newly added Chinese text | Edit UTF-8 files and check translations for replacement characters | tests/i18n.test.mjs |
| Manual retries must not replay the same failed record indefinitely | Mark the original retried and create the new pending delivery in one transaction | TestRetryCannotBeReplayed |
| Windows keep-awake state belongs to the calling thread | Enable and release it on the same locked OS thread; terminate that thread if cleanup fails | TestWindowsKeepAwakeThreadLifecycle |
| Headless/browser tests cannot replace desktop testing | Test the release EXE's native lifecycle and connect a separate test build to real WebView2 | TestNativeDesktopLifecycle, tests/desktop-e2e.mjs |
| A tray lookup can return both an empty result and no error | Check process ownership, errors, and a nonempty rectangle when locating the tray window | TestNativeDesktopLifecycle |
| Windows login commands can contain spaces, Chinese characters, and trailing backslashes | Use system argument quoting rules and validate the Run registry command length | TestWindowsAutostartCommandRoundTrip |
| Notification text must never be interpolated into scripts | XML-encode the text and send it over stdin to a fixed script | TestWindowsToastContentIsData |
| Copying the design CSS and then overriding it in an app stylesheet silently changes the design (62 px header, 21 px titles, text buttons) | app.css only adds layout and states; component sizes, spacing and colours stay in the design's loop.css | tests/screens.mjs screenshot matrix reviewed against doc/loopbee-design/png |
| A fixed 1280×900 window does not fit 1080p at 150 % or 1366×768 | Clamp the first frame to the screen work area and remember bounds | TestFitWindow |
| The page CSP forbids inline style attributes, so design markup like `style="--c:…"` silently does nothing | Put per-element variables in data-* and apply them through CSSOM after render | tests/e2e.mjs (no script errors, bee animation asserted) |
| Lifecycle and business outcome in one badge hides that "ended" ≠ "goal reached" | Badge shows lifecycle; a separate chip shows the outcome (spec §5) | tests/screens.mjs, tests/e2e.mjs |
| A timeline reconstructed from free-text events is guesswork | The runner records one trace entry per executed step | TestRunTraceRecordsRoundsGatesAndArtifacts |
| Teams already have complete agent workflows; a step-by-step editor duplicated them and blocked adoption | Default to one agent task + optional gate, authored by chat with the bound CLI; the form stays for experts | tests/agents-e2e.mjs |
| A sandbox without network silently breaks git push, deploys and databases | Make network an explicit permission level mapped per CLI; show the exact flags | TestBuildAgentArgs |
| Counting gate waiting toward the run time limit times out overnight approvals | Budget only step execution; give gates their own expiry | TestGateWaitIsOutsideTheTimeBudgetAndExpires |
| “Agent exited 0” cannot distinguish done, nothing-to-do and blocked | Append a result contract and parse the last LOOPBEE_RESULT line | TestAgentStepOutcomesForBothCLIs |
| Agents switching branches in the developer's checkout collide with their work | Optional per-Run git worktree from a base ref | TestWorktreeWorkspaceLeavesTheCheckoutAlone |
| An agent proposal could smuggle enabled schedules or secret values | Normalise proposals: drafts stay disabled, secret values are dropped, Validate and the ceiling still apply | TestAuthorProposalIsNormalisedDraft, TestBeectlCreatesDisabledDraftAndRuns |
| A real CLI with a missing sandbox fails with a cryptic generic error | Surface the CLI's own error text in the Run | TestAgentFailureReason |
