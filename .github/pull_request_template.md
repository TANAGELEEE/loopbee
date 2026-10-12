## What changed

<!-- One or two sentences, for the reviewer. Link the issue: "Fixes #123". -->

## Verification

- [ ] `go vet ./... && go test ./...`
- [ ] `npm test && npm run test:e2e` (UI changes need an E2E step, not only a screenshot)
- [ ] Platform build or CI run for `scripts/build-*.{sh,ps1}` changes
- [ ] `sh scripts/real-smoke.sh` for agent adapter, authoring, workspace or result-parsing changes

Not verified:

<!-- List what you did not run. "Nothing" is a valid answer only if every box above applies and is checked. -->

## Lessons

- [ ] This fixes a defect: the failing test is ______ and LESSONS.md has a new lesson or a new Seen date
- [ ] No defect fixed

<!-- User-visible fixes also go into docs/releases/<next tag>.md under 修正問題. -->
