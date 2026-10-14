//go:build !windows

package core

import (
	"bufio"
	"context"
	"strings"
	"time"
)

// savedUserEnv asks the user's login shell for one variable. name has been checked to be a plain identifier.
func savedUserEnv(name string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	out, err := runCommand(ctx, "", []string{loginShell(), "-ilc", `printf '\n__LOOPBEE_ENV__%s\n' "$` + name + `"`}, nil)
	if err != nil && len(out) == 0 {
		return "", false
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "__LOOPBEE_ENV__"); ok && v != "" {
			return v, true
		}
	}
	return "", false
}
