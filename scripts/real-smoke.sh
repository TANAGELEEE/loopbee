#!/bin/sh
# Real end-to-end check on this Mac: package loopbee.app, run the Go tests, then drive the packaged
# binary with your own agent CLIs — Codex CLI, Claude Code, Gemini CLI, OpenCode, pi (uses your subscriptions for
# a few short agent runs).
# Everything runs against a throw-away sample repository in a temp folder; your projects are not touched.
# The full log is written to .test-data/real-smoke.log; afterwards the packaged app opens on the test data.
#
#   sh scripts/real-smoke.sh                      # all detected agents, both scenarios
#   sh scripts/real-smoke.sh --agents claude      # one agent
#   sh scripts/real-smoke.sh --agents gemini,opencode,pi
#   sh scripts/real-smoke.sh --scenarios report   # read-only scenario only
set -u
cd "$(dirname "$0")/.."
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-11.0}"
export CGO_CFLAGS="${CGO_CFLAGS:--O2 -g} -mmacosx-version-min=$MACOSX_DEPLOYMENT_TARGET"
export CGO_LDFLAGS="${CGO_LDFLAGS:--O2 -g} -mmacosx-version-min=$MACOSX_DEPLOYMENT_TARGET"
mkdir -p .test-data
rm -f .test-data/real-smoke.status
log=.test-data/real-smoke.log
(
  set -e
  echo "== loopbee real smoke test · $(date '+%Y-%m-%d %H:%M:%S %Z')"
  sw_vers 2>/dev/null || true
  uname -m
  echo "== tools"
  go version
  node --version
  git --version
  for a in codex claude gemini opencode pi; do
    if command -v "$a" >/dev/null 2>&1; then echo "$a: $(command -v "$a") · $("$a" --version 2>&1 | head -1)"; else echo "$a: not on PATH"; fi
  done
  echo "== go test"
  go test ./... 2>&1
  echo "== package"
  sh scripts/build-macos.sh
  echo "== real agents"
  node tests/real-smoke.mjs --keep "$@"
  echo ok > .test-data/real-smoke.status
) 2>&1 | tee "$log"
if [ -f .test-data/real-smoke.status ]; then status=0; rm -f .test-data/real-smoke.status; else status=1; fi
echo "== $( [ $status = 0 ] && echo PASSED || echo FAILED ) · log saved to $log"
# Open the packaged app on the smoke test's data so the Loops and Runs created by the real agents can be
# reviewed in the real window (set LOOPBEE_SMOKE_NO_OPEN=1 to skip).
kept=$(sed -n 's/^kept: //p' "$log" | tail -1)
if [ -n "$kept" ] && [ -d "$kept/data" ] && [ -z "${LOOPBEE_SMOKE_NO_OPEN:-}" ] && [ -d bin/loopbee.app ]; then
  echo "== opening bin/loopbee.app with $kept/data"
  open -n bin/loopbee.app --args -data "$kept/data"
fi
exit $status
