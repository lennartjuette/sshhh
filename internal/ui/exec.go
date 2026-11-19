package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// pager returns a command that shows a file, starting at its end.
func pager(path string) *exec.Cmd {
	if cmd := fromEnv(path, "PAGER"); cmd != nil {
		return cmd
	}
	if runtime.GOOS == "windows" {
		return exec.Command("more", path)
	}
	return exec.Command("less", "+G", path)
}

// editor returns a command that opens a file for editing: $VISUAL or
// $EDITOR if set, otherwise Notepad on Windows and nano or vi elsewhere.
func editor(path string) *exec.Cmd {
	if cmd := fromEnv(path, "VISUAL", "EDITOR"); cmd != nil {
		return cmd
	}
	if runtime.GOOS == "windows" {
		return exec.Command("notepad", path)
	}
	if _, err := exec.LookPath("nano"); err == nil {
		return exec.Command("nano", path)
	}
	return exec.Command("vi", path)
}

// fromEnv builds a command from the first set variable, which may contain
// arguments ("code --wait").
func fromEnv(path string, vars ...string) *exec.Cmd {
	for _, v := range vars {
		if f := strings.Fields(os.Getenv(v)); len(f) > 0 {
			return exec.Command(f[0], append(f[1:], path)...)
		}
	}
	return nil
}
