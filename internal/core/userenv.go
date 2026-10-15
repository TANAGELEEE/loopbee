package core

import (
	"os"
	"regexp"
)

// userEnv looks up a variable the way the user's own terminal sees it. An app started from the Dock, Finder, the
// tray or at login does not inherit what the shell profile exports (tokens, AWS_PROFILE, …), and on Windows it keeps
// the values from when it started; so when loopbee's own environment lacks the variable it asks the login shell
// (macOS, Linux) or the saved user and system variables (Windows).
func userEnv(name string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v, true
	}
	if !anyEnvName.MatchString(name) {
		return "", false
	}
	return savedUserEnv(name)
}

var anyEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
