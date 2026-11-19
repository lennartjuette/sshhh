//go:build !windows

package tunnel

import (
	"os/exec"
	"syscall"
)

// detach runs cmd in a new session, so it keeps running after sshhh exits
// and does not get the terminal's SIGINT or SIGHUP.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
