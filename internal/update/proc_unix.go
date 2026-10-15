//go:build !windows

package update

import (
	"os/exec"
	"syscall"
)

var errCrossDevice = syscall.EXDEV

// alive reports whether pid is still running.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// detach keeps the relaunch out of this process group, so it outlives it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
