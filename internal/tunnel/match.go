// Package tunnel matches configured tunnels to running ssh processes and
// starts, stops and restarts them.
package tunnel

import (
	"github.com/lennartjuette/sshhh/internal/config"
	"github.com/lennartjuette/sshhh/internal/proc"
	"github.com/lennartjuette/sshhh/internal/sshcmd"
)

// Status is the state of a tunnel as shown to the user.
type Status int

const (
	Down     Status = iota
	Starting        // process runs, but not all local ports listen yet
	Up
	Failed // sshhh started it and it exited with an error
)

func (s Status) String() string {
	return [...]string{"down", "starting", "up", "failed"}[s]
}

// Row is one line of the tunnel list.
type Row struct {
	Name     string
	Config   *config.Tunnel // nil if the process is not in the config
	Proc     *proc.Proc     // nil if no matching process was found
	PID      int32          // running process, or the child sshhh just started; 0 if none
	Status   Status
	ExitCode int // set when Status is Failed; -1 if killed by a signal
}

// Match pairs every configured tunnel with a running ssh process that goes
// to the same host with the same forwards, in config order. Tunnel processes
// that match nothing follow as rows without config. Plain ssh sessions
// without forwards are left out.
func Match(tunnels []config.Tunnel, procs []proc.Proc) []Row {
	used := make(map[int32]bool)
	rows := make([]Row, 0, len(tunnels))
	for i := range tunnels {
		t := &tunnels[i]
		spec := t.Spec()
		row := Row{Name: t.Name, Config: t}
		for j := range procs {
			p := &procs[j]
			if !used[p.PID] && p.IsTunnel() &&
				sshcmd.Host(p.Dest) == sshcmd.Host(spec.Host) &&
				sshcmd.SameForwards(p.Forwards, spec.Forwards) {
				used[p.PID] = true
				row.Proc = p
				break
			}
		}
		rows = append(rows, row)
	}
	for j := range procs {
		if p := &procs[j]; p.IsTunnel() && !used[p.PID] {
			rows = append(rows, Row{Name: sshcmd.Host(p.Dest), Proc: p})
		}
	}
	return rows
}

// listensOnAll reports whether every local forward of p has its port open.
func listensOnAll(p *proc.Proc) bool {
	if p.Listening == nil {
		return true // unknown: trust that the process is fine
	}
	for _, f := range p.Forwards {
		if port, ok := f.ListenPort(); ok && !p.Listening[port] {
			return false
		}
	}
	return true
}
