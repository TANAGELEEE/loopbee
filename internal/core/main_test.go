package core

import (
	"os"
	"strings"
	"testing"
)

// The fake CLIs are this test binary started again. Built with -race, every such process sleeps a second at exit
// (atexit_sleep_ms defaults to 1000), which pushed the package past go test's 10-minute limit in CI.
func TestMain(m *testing.M) {
	if g := os.Getenv("GORACE"); !strings.Contains(g, "atexit_sleep_ms") {
		os.Setenv("GORACE", strings.TrimSpace(g+" atexit_sleep_ms=0"))
	}
	os.Exit(m.Run())
}
