// Package proc finds the user's running ssh processes and their TCP sockets.
package proc

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/lennartjuette/sshhh/internal/sshcmd"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Proc is a running ssh process.
type Proc struct {
	PID      int32
	Started  time.Time
	Forwards []sshcmd.Forward
	Dest     string
	// Listening holds the local ports in LISTEN state. It is nil if the
	// sockets could not be read, which callers should treat as "unknown".
	Listening map[int]bool
	// Clients counts established connections on the listening ports.
	Clients int
}

// IsTunnel reports whether the process forwards any ports.
func (p Proc) IsTunnel() bool { return len(p.Forwards) > 0 }

// Snapshot returns the ssh processes of the current user. Sockets are only
// inspected for tunnels, since reading them is the expensive part.
func Snapshot(ctx context.Context) ([]Proc, error) {
	all, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	uid := os.Getuid() // -1 on Windows, where processes of others are not readable anyway
	var procs []Proc
	for _, p := range all {
		if name, err := p.NameWithContext(ctx); err != nil || !isSSH(name) {
			continue
		}
		if uid >= 0 {
			if uids, err := p.UidsWithContext(ctx); err != nil || len(uids) == 0 || int(uids[0]) != uid {
				continue
			}
		}
		argv, err := p.CmdlineSliceWithContext(ctx)
		if err != nil || len(argv) == 0 {
			continue
		}
		pr := Proc{PID: p.Pid}
		pr.Forwards, pr.Dest = sshcmd.Parse(argv)
		if ms, err := p.CreateTimeWithContext(ctx); err == nil {
			pr.Started = time.UnixMilli(ms)
		}
		if pr.IsTunnel() {
			if conns, err := net.ConnectionsPidWithContext(ctx, "tcp", p.Pid); err == nil {
				pr.Listening, pr.Clients = countSockets(conns)
			}
		}
		procs = append(procs, pr)
	}
	return procs, nil
}

func isSSH(name string) bool {
	name = strings.ToLower(name)
	return name == "ssh" || name == "ssh.exe"
}

// countSockets returns the listening ports and the number of established
// connections on them. Connections to the ssh server are not counted.
func countSockets(conns []net.ConnectionStat) (map[int]bool, int) {
	listening := make(map[int]bool)
	for _, c := range conns {
		if c.Status == "LISTEN" {
			listening[int(c.Laddr.Port)] = true
		}
	}
	clients := 0
	for _, c := range conns {
		if c.Status == "ESTABLISHED" && listening[int(c.Laddr.Port)] {
			clients++
		}
	}
	return listening, clients
}

// Terminate asks a process to exit: SIGTERM on Unix, TerminateProcess on Windows.
func Terminate(pid int32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	return p.Terminate()
}

// Kill ends a process immediately.
func Kill(pid int32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// Exists reports whether a process with this PID is running.
func Exists(pid int32) bool {
	ok, err := process.PidExists(pid)
	return ok && err == nil
}
