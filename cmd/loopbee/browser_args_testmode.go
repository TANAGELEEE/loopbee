//go:build windows && desktoptest && !nogui

package main

import (
	"os"
	"strconv"
)

func desktopBrowserArgs() []string {
	port, err := strconv.Atoi(os.Getenv("LOOPBEE_TEST_CDP_PORT"))
	if err != nil || port < 1 || port > 65535 {
		return nil
	}
	return []string{"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=" + strconv.Itoa(port)}
}
