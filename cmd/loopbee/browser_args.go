//go:build (darwin || windows) && !nogui && (!desktoptest || !windows)

package main

func desktopBrowserArgs() []string { return nil }
