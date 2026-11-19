//go:build windows

package tunnel

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps ssh.exe from opening a console window.
const createNoWindow = 0x08000000

// detach runs cmd in its own process group without a console window, so it
// keeps running after sshhh exits and does not get the terminal's Ctrl+C.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createNoWindow,
	}
}
