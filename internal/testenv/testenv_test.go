package testenv

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// Codex's sandbox on macOS answers bind with "operation not permitted"; such tests skip locally and fail in CI.
func TestLoopbackSkipsOnlyInsideASandbox(t *testing.T) {
	denied := fmt.Errorf("listen tcp 127.0.0.1:0: bind: %w", syscall.EPERM)
	if skip, msg := loopbackDecision(denied, false); !skip || !strings.Contains(msg, "CI runs this test") {
		t.Fatalf("sandbox: skip=%v %q", skip, msg)
	}
	if skip, msg := loopbackDecision(denied, true); skip || msg == "" {
		t.Fatal("CI must fail, not skip")
	}
	if skip, msg := loopbackDecision(fmt.Errorf("listen: %w", syscall.EADDRINUSE), false); skip || msg == "" {
		t.Fatal("other listen errors are failures")
	}
	if skip, msg := loopbackDecision(nil, false); skip || msg != "" {
		t.Fatal("a free port runs the test")
	}
}
