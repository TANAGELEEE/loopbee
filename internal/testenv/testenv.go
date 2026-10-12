// Package testenv holds environment checks shared by tests.
package testenv

import (
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
)

// Loopback skips a test that needs a local port when the process is not allowed to open one. That is the case
// inside the workspace-write sandbox Loopbee runs coding agents in (Codex, Claude Code), including Loopbee's own
// self-improvement Loop. CI never skips: there a refused port is a failure.
func Loopback(t testing.TB) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		l.Close()
	}
	skip, msg := loopbackDecision(err, os.Getenv("CI") != "")
	if skip {
		t.Skip(msg)
	} else if msg != "" {
		t.Fatal(msg)
	}
}

func loopbackDecision(err error, ci bool) (skip bool, msg string) {
	switch {
	case err == nil:
		return false, ""
	case !ci && (errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)):
		return true, "needs a local port, which this sandbox does not allow (" + err.Error() + "); CI runs this test"
	default:
		return false, "cannot open a local port: " + err.Error()
	}
}
