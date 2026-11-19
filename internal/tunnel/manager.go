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

// Watchdog timing: the first retry comes after minRetry, every failed one
// doubles the wait up to maxRetry, and a tunnel that stays up for stableAfter
// starts from minRetry again the next time it drops.
const (
	minRetry    = time.Second
	maxRetry    = time.Minute
	stableAfter = 30 * time.Second
)

// retry tracks the watchdog's attempts for one tunnel.
type retry struct {
	attempts int
	next     time.Time // when to try again; zero while an attempt runs
	upSince  time.Time // when the tunnel came up after the last attempt
}

// Manager starts and stops tunnels and remembers the processes it started.
// Started tunnels run in their own session, so they outlive sshhh.
// All methods are safe for concurrent use.
type Manager struct {
	ssh    string
	logDir string
	now    func() time.Time // replaced in tests

	mu       sync.Mutex
	children map[string]*child // by tunnel name, latest start only
	exits    []Exit
	want     map[string]bool   // tunnels that should run, by name
	retries  map[string]*retry // watchdog state, by name
	stopped  map[int32]bool    // PIDs sshhh asked to exit
}

// NewManager returns a Manager that runs the given ssh binary and writes one
// log file per tunnel into logDir.
func NewManager(ssh, logDir string) *Manager {
	return &Manager{
		ssh: ssh, logDir: logDir, now: time.Now,
		children: make(map[string]*child),
		want:     make(map[string]bool),
		retries:  make(map[string]*retry),
		stopped:  make(map[int32]bool),
	}
}

// Rows matches tunnels to procs and fills in each row's status, taking the
// processes this Manager started and stopped into account. procs may be a
// few seconds old; Rows corrects what it knows better.
func (m *Manager) Rows(tunnels []config.Tunnel, procs []proc.Proc) []Row {
	all := Match(tunnels, procs)
	rows := all[:0]
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range all {
		var c *child
		if r.Config != nil {
			c = m.children[r.Name]
		}
		if r.Proc != nil && (m.stopped[r.Proc.PID] || c != nil && c.exited && c.pid == r.Proc.PID) {
			r.Proc = nil // stale snapshot: it is gone or on its way out
		}
		if r.Config == nil && r.Proc == nil {
			continue // an unknown tunnel we stopped
		}
		rt := m.retries[r.Name]
		switch {
		case r.Proc != nil:
			r.PID, r.Status = r.Proc.PID, Starting
			if listensOnAll(r.Proc) {
				r.Status = Up
			}
		case c != nil && !c.exited:
			r.PID, r.Status = c.pid, Starting
		case r.Config != nil && r.Config.Reconnects() && m.want[r.Name]:
			r.Status = Reconnecting
			if rt != nil {
				r.Attempt, r.RetryAt = rt.attempts, rt.next
			}
		case c != nil && !c.stopping && c.code != 0:
			r.Status, r.ExitCode = Failed, c.code
		}
		rows = append(rows, r)
	}
	return rows
}

// Watchdog keeps configured tunnels running. A tunnel that runs is marked as
// wanted; when a wanted tunnel is gone, Watchdog starts it again, waiting
// longer after every failed attempt. Stop, StopAll and KillAll end the
// wish. Call Watchdog regularly with fresh rows; it returns the names of the
// tunnels it started.
func (m *Manager) Watchdog(rows []Row) []string {
	now := m.now()
	var due []config.Tunnel
	m.mu.Lock()
	for _, r := range rows {
		if r.Config == nil || !r.Config.Reconnects() {
			continue
		}
		rt := m.retries[r.Name]
		switch {
		case r.PID != 0:
			m.want[r.Name] = true
			if rt == nil || r.Status != Up {
				continue
			}
			if rt.upSince.IsZero() {
				rt.upSince = now
			} else if now.Sub(rt.upSince) >= stableAfter {
				delete(m.retries, r.Name)
			}
		case m.want[r.Name]:
			if rt == nil {
				rt = &retry{}
				m.retries[r.Name] = rt
			}
			rt.upSince = time.Time{}
			if rt.next.IsZero() { // it just went down: schedule the next try
				rt.next = now.Add(backoff(rt.attempts))
			}
			if now.Before(rt.next) {
				continue
			}
			rt.attempts++
			rt.next = time.Time{}
			due = append(due, *r.Config)
		}
	}
	m.mu.Unlock()

	var started []string
	for _, t := range due {
		if m.launch(t) == nil {
			started = append(started, t.Name)
		}
	}
	return started
}

// backoff returns the wait before the next attempt after n failed ones.
func backoff(n int) time.Duration {
	if n >= 6 { // 1s << 6 already exceeds maxRetry
		return maxRetry
	}
	return min(minRetry<<n, maxRetry)
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

// Start launches ssh for t in the background and lets the watchdog look
// after it. Output goes to its log file.
func (m *Manager) Start(t config.Tunnel) error {
	m.mu.Lock()
	m.want[t.Name] = true
	delete(m.retries, t.Name) // a manual start begins a fresh count
	m.mu.Unlock()
	return m.launch(t)
}

func (m *Manager) launch(t config.Tunnel) error {
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

// Stop asks the row's process to exit and tells the watchdog to leave the
// tunnel alone, which also ends a pending reconnect.
func (m *Manager) Stop(r Row) error {
	m.mu.Lock()
	delete(m.want, r.Name)
	delete(m.retries, r.Name)
	m.mu.Unlock()
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

// KillAll terminates every given ssh process, tunnel or not, and stops the
// watchdog from bringing any of them back.
func (m *Manager) KillAll(procs []proc.Proc) int {
	m.mu.Lock()
	clear(m.want)
	clear(m.retries)
	m.mu.Unlock()
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
	m.stopped[pid] = true
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

// lastLine returns the last non-empty line of a file, reading only its end,
// or "" if that is the header Start writes before each run.
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
	last := strings.TrimSpace(string(lines[len(lines)-1]))
	if strings.HasPrefix(last, "=== ") {
		return "" // our own header: ssh said nothing
	}
	return last
}
