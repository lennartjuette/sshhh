package tunnel

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lennartjuette/sshhh/internal/config"
	"github.com/lennartjuette/sshhh/internal/proc"
)

// Exit reports a tunnel started by sshhh that ended without being asked to.
type Exit struct {
	Name    string
	Code    int
	LastLog string // last line ssh wrote, usually the reason
}

// child is an ssh process started by this Manager.
type child struct {
	pid      int32
	done     chan struct{} // closed when the process has exited
	exited   bool
	code     int
	stopping bool // exit was requested, so it is not a failure
}

// Manager starts and stops tunnels and remembers the processes it started.
// Started tunnels run in their own session, so they outlive sshhh.
// All methods are safe for concurrent use.
type Manager struct {
	ssh    string
	logDir string

	mu       sync.Mutex
	children map[string]*child // by tunnel name, latest start only
	exits    []Exit
}

// NewManager returns a Manager that runs the given ssh binary and writes one
// log file per tunnel into logDir.
func NewManager(ssh, logDir string) *Manager {
	return &Manager{ssh: ssh, logDir: logDir, children: make(map[string]*child)}
}

// Rows matches tunnels to procs and fills in each row's status, taking the
// processes this Manager started into account. procs may be a few seconds
// old; Rows corrects what it knows better.
func (m *Manager) Rows(tunnels []config.Tunnel, procs []proc.Proc) []Row {
	rows := Match(tunnels, procs)
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range rows {
		r := &rows[i]
		var c *child
		if r.Config != nil {
			c = m.children[r.Name]
		}
		if r.Proc != nil && c != nil && c.exited && c.pid == r.Proc.PID {
			r.Proc = nil // stale snapshot: we saw it exit
		}
		switch {
		case r.Proc != nil:
			r.PID, r.Status = r.Proc.PID, Starting
			if listensOnAll(r.Proc) {
				r.Status = Up
			}
		case c != nil && !c.exited:
			r.PID, r.Status = c.pid, Starting
		case c != nil && !c.stopping && c.code != 0:
			r.Status, r.ExitCode = Failed, c.code
		}
	}
	return rows
}

// LogPath returns the log file of the named tunnel.
func (m *Manager) LogPath(name string) string {
	slug := strings.Trim(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return '-'
	}, name), "-")
	return filepath.Join(m.logDir, slug+".log")
}

// Start launches ssh for t in the background. Output goes to its log file.
func (m *Manager) Start(t config.Tunnel) error {
	if err := os.MkdirAll(m.logDir, 0o700); err != nil {
		return err
	}
	path := m.LogPath(t.Name)
	log, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close() // the child keeps its own copy

	args := t.Spec().Args()
	fmt.Fprintf(log, "\n=== %s %s %s\n", time.Now().Format(time.DateTime), m.ssh, strings.Join(args, " "))
	cmd := exec.Command(m.ssh, args...)
	cmd.Stdout, cmd.Stderr = log, log
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", t.Name, err)
	}

	c := &child{pid: int32(cmd.Process.Pid), done: make(chan struct{})}
	m.mu.Lock()
	m.children[t.Name] = c
	m.mu.Unlock()
	go m.wait(t.Name, c, cmd, path)
	return nil
}

func (m *Manager) wait(name string, c *child, cmd *exec.Cmd, logPath string) {
	_ = cmd.Wait() // the exit code is all we need
	code := cmd.ProcessState.ExitCode()
	m.mu.Lock()
	defer m.mu.Unlock()
	c.exited, c.code = true, code
	close(c.done)
	if !c.stopping && code != 0 {
		m.exits = append(m.exits, Exit{Name: name, Code: code, LastLog: lastLine(logPath)})
	}
}

// Exits returns the unexpected exits since the last call.
func (m *Manager) Exits() []Exit {
	m.mu.Lock()
	defer m.mu.Unlock()
	exits := m.exits
	m.exits = nil
	return exits
}

// Stop asks the row's process to exit. It does nothing if none runs.
func (m *Manager) Stop(r Row) error {
	if r.PID == 0 {
		return nil
	}
	m.markStopping(r.PID)
	return proc.Terminate(r.PID)
}

// StopAll stops all rows that have a process and returns how many there were.
func (m *Manager) StopAll(rows []Row) int {
	n := 0
	for _, r := range rows {
		if r.PID != 0 && m.Stop(r) == nil {
			n++
		}
	}
	return n
}

// KillAll terminates every given ssh process, tunnel or not.
func (m *Manager) KillAll(procs []proc.Proc) int {
	n := 0
	for _, p := range procs {
		m.markStopping(p.PID)
		if proc.Terminate(p.PID) == nil {
			n++
		}
	}
	return n
}

// Restart stops the row's process, waits for it to exit (killing it after
// a few seconds) and starts the tunnel again.
func (m *Manager) Restart(r Row) error {
	if r.Config == nil {
		return fmt.Errorf("%s is not in the config, so it cannot be restarted", r.Name)
	}
	if r.PID != 0 {
		if err := m.Stop(r); err != nil && proc.Exists(r.PID) {
			return err
		}
		if !m.waitGone(r.PID, 3*time.Second) {
			_ = proc.Kill(r.PID)
			m.waitGone(r.PID, time.Second)
		}
	}
	return m.Start(*r.Config)
}

func (m *Manager) markStopping(pid int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.children {
		if c.pid == pid {
			c.stopping = true
		}
	}
}

// waitGone waits until the process has exited. For our own children it
// waits on Wait, since an unreaped child still exists as a zombie.
func (m *Manager) waitGone(pid int32, timeout time.Duration) bool {
	var done chan struct{}
	m.mu.Lock()
	for _, c := range m.children {
		if c.pid == pid {
			done = c.done
		}
	}
	m.mu.Unlock()

	deadline := time.After(timeout)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if done == nil && !proc.Exists(pid) {
			return true
		}
		select {
		case <-done: // a nil channel blocks, so this only fires for children
			return true
		case <-deadline:
			return false
		case <-tick.C:
		}
	}
}

// lastLine returns the last non-empty line of a file, reading only its end.
func lastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > 4096 {
		_, _ = f.Seek(-4096, io.SeekEnd)
	}
	tail, _ := io.ReadAll(f)
	lines := bytes.Split(bytes.TrimSpace(tail), []byte("\n"))
	return strings.TrimSpace(string(lines[len(lines)-1]))
}
