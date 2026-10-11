//go:build windows && desktoptest && !nogui

package main

import (
	"reflect"
	"testing"
)

func TestDesktopBrowserArgs(t *testing.T) {
	for _, port := range []string{"", "0", "-1", "65536", "9222 --remote-debugging-address=0.0.0.0"} {
		t.Setenv("LOOPBEE_TEST_CDP_PORT", port)
		if got := desktopBrowserArgs(); got != nil {
			t.Fatalf("invalid port %q enabled debugging: %v", port, got)
		}
	}
	t.Setenv("LOOPBEE_TEST_CDP_PORT", "9222")
	want := []string{"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=9222"}
	if got := desktopBrowserArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
