package tunnel

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennartjuette/sshhh/internal/config"
	"github.com/lennartjuette/sshhh/internal/proc"
)

// The test binary doubles as a fake ssh: with SSHHH_FAKE_SSH set it plays
// a tunnel that runs until killed, or one that fails right away.
func TestMain(m *testing.M) {
	switch os.Getenv("SSHHH_FAKE_SSH") {
	case "run":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "ssh: Could not resolve hostname nope: nodename nor servname provided")
		os.Exit(255)
	}
	os.Exit(m.Run())
}

func fakeManager(t *testing.T, mode string) (*Manager, config.Tunnel) {
	t.Helper()
	t.Setenv("SSHHH_FAKE_SSH", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(exe, t.TempDir()), config.Tunnel{Name: "Test Tunnel", Host: "gw", Socks: 1080}
}

// row returns the tunnel's row as the UI would see it before the next scan.
func row(m *Manager, tun config.Tunnel) Row {
	return m.Rows([]config.Tunnel{tun}, nil)[0]
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStartStop(t *testing.T) {
	m, tun := fakeManager(t, "run")
	if err := m.Start(tun); err != nil {
		t.Fatal(err)
	}
	r := row(m, tun)
	if r.Status != Starting || r.PID == 0 {
		t.Fatalf("after start: %+v", r)
	}
	if !proc.Exists(r.PID) {
		t.Fatal("child is not running")
	}

	if err := m.Stop(r); err != nil {
		t.Fatal(err)
	}
	eventually(t, "stop", func() bool { return row(m, tun).Status == Down })
	if exits := m.Exits(); len(exits) != 0 {
		t.Errorf("a requested stop was reported as exit: %+v", exits)
	}

	log, err := os.ReadFile(m.LogPath(tun.Name))
	if err != nil || !strings.Contains(string(log), "-D 1080 gw") {
		t.Errorf("log does not show the command: %q, %v", log, err)
	}
}

func TestFailure(t *testing.T) {
	m, tun := fakeManager(t, "fail")
	off := false
	tun.Reconnect = &off
	if err := m.Start(tun); err != nil {
		t.Fatal(err)
	}
	eventually(t, "failure", func() bool { return row(m, tun).Status == Failed })
	if r := row(m, tun); r.ExitCode != 255 {
		t.Errorf("exit code %d; want 255", r.ExitCode)
	}
	exits := m.Exits()
	if len(exits) != 1 || exits[0].Name != tun.Name || !strings.Contains(exits[0].LastLog, "Could not resolve") {
		t.Errorf("exits = %+v", exits)
	}
	if len(m.Exits()) != 0 {
		t.Error("Exits must drain")
	}
}

func TestRestart(t *testing.T) {
	m, tun := fakeManager(t, "run")
	if err := m.Start(tun); err != nil {
		t.Fatal(err)
	}
	old := row(m, tun)
	if err := m.Restart(old); err != nil {
		t.Fatal(err)
	}
	r := row(m, tun)
	if r.PID == old.PID || r.Status != Starting {
		t.Errorf("after restart: %+v (old pid %d)", r, old.PID)
	}
	if proc.Exists(old.PID) {
		t.Error("old process still runs")
	}
	m.StopAll([]Row{r})
	eventually(t, "stop", func() bool { return row(m, tun).Status == Down })

	if err := m.Restart(Row{Name: "external", PID: 1}); err == nil {
		t.Error("restarting a tunnel without config must fail")
	}
}

func TestStaleSnapshot(t *testing.T) {
	m, tun := fakeManager(t, "run")
	if err := m.Start(tun); err != nil {
		t.Fatal(err)
	}
	r := row(m, tun)
	snapshot := []proc.Proc{{PID: r.PID, Dest: "gw", Forwards: tun.Spec().Forwards}}
	_ = m.Stop(r)
	eventually(t, "exit", func() bool { return m.Rows([]config.Tunnel{tun}, snapshot)[0].Status == Down })
}

func TestLogPath(t *testing.T) {
	m := NewManager("ssh", "logs")
	if got := m.LogPath("Office VPN / Prod!"); !strings.HasSuffix(got, "office-vpn---prod.log") {
		t.Errorf("LogPath = %q", got)
	}
}

func TestLastLine(t *testing.T) {
	path := t.TempDir() + "/log"
	content := strings.Repeat("x", 5000) + "\nfirst\nlast line\n\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := lastLine(path); got != "last line" {
		t.Errorf("lastLine = %q", got)
	}
	if err := os.WriteFile(path, []byte("old error\n\n=== 2026-01-01 ssh -N gw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := lastLine(path); got != "" {
		t.Errorf("the start header is not a reason: lastLine = %q", got)
	}
	if lastLine(path+".missing") != "" {
		t.Error("missing file must give empty line")
	}
}

// clock is a fake time source for the watchdog.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func watch(m *Manager, tun config.Tunnel) []string {
	return m.Watchdog(m.Rows([]config.Tunnel{tun}, nil))
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for n, w := range want {
		if got := backoff(n); got != w*time.Second {
			t.Errorf("backoff(%d) = %v; want %v", n, got, w*time.Second)
		}
	}
	if backoff(1000) != maxRetry {
		t.Error("large attempt counts must not overflow")
	}
}

func TestWatchdogRetriesWithBackoff(t *testing.T) {
	m, tun := fakeManager(t, "fail")
	clk := &clock{t: time.Unix(1000, 0)}
	m.now = clk.now
	if err := m.Start(tun); err != nil {
		t.Fatal(err)
	}
	for attempt, wait := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		eventually(t, "exit", func() bool { return row(m, tun).PID == 0 })
		if got := watch(m, tun); got != nil {
			t.Fatalf("attempt %d: restarted at once, want a wait of %v", attempt, wait)
		}
		r := row(m, tun)
		if r.Status != Reconnecting || r.Attempt != attempt || !r.RetryAt.Equal(clk.t.Add(wait)) {
			t.Fatalf("attempt %d: %+v; want reconnecting at +%v", attempt, r, wait)
		}
		clk.advance(wait - time.Millisecond)
		if got := watch(m, tun); got != nil {
			t.Fatalf("attempt %d: restarted before the wait was over", attempt)
		}
		clk.advance(time.Millisecond)
		if got := watch(m, tun); len(got) != 1 || got[0] != tun.Name {
			t.Fatalf("attempt %d: Watchdog = %v; want a restart", attempt, got)
		}
	}
	_ = m.Stop(row(m, tun))
}

func TestWatchdogLeavesStoppedTunnels(t *testing.T) {
	m, tun := fakeManager(t, "run")
	clk := &clock{t: time.Unix(1000, 0)}
	m.now = clk.now
	if err := m.Start(tun); err != nil {
		t.Fatal(err)
	}
	_ = m.Stop(row(m, tun))
	eventually(t, "stop", func() bool { return row(m, tun).Status == Down })
	clk.advance(time.Hour)
	if got := watch(m, tun); got != nil {
		t.Errorf("Watchdog restarted a stopped tunnel: %v", got)
	}
}

func TestWatchdogStopCancelsReconnect(t *testing.T) {
	m, tun := fakeManager(t, "fail")
	m.now = (&clock{t: time.Unix(1000, 0)}).now
	_ = m.Start(tun)
	eventually(t, "exit", func() bool { return row(m, tun).PID == 0 })
	watch(m, tun)
	if r := row(m, tun); r.Status != Reconnecting {
		t.Fatalf("status %v; want reconnecting", r.Status)
	}
	_ = m.Stop(row(m, tun))
	if r := row(m, tun); r.Status == Reconnecting {
		t.Errorf("Stop must end the reconnect, status is %v", r.Status)
	}
}

func TestWatchdogAdoptsRunningTunnels(t *testing.T) {
	m, tun := fakeManager(t, "run")
	clk := &clock{t: time.Unix(1000, 0)}
	m.now = clk.now
	running := []proc.Proc{{PID: 4242, Dest: "gw", Forwards: tun.Spec().Forwards}}
	m.Watchdog(m.Rows([]config.Tunnel{tun}, running)) // seen running: wanted from now on

	if got := watch(m, tun); got != nil { // gone from the next snapshot
		t.Fatal("restarted without waiting")
	}
	clk.advance(time.Second)
	if got := watch(m, tun); len(got) != 1 {
		t.Fatalf("Watchdog = %v; want the adopted tunnel restarted", got)
	}
	_ = m.Stop(row(m, tun))
}

func TestWatchdogResetsAfterStableUptime(t *testing.T) {
	m, tun := fakeManager(t, "run")
	clk := &clock{t: time.Unix(1000, 0)}
	m.now = clk.now
	up := func() []Row {
		return m.Rows([]config.Tunnel{tun}, []proc.Proc{{PID: 4242, Dest: "gw", Forwards: tun.Spec().Forwards}})
	}
	m.Watchdog(up())
	m.mu.Lock()
	m.retries[tun.Name] = &retry{attempts: 5}
	m.mu.Unlock()

	m.Watchdog(up())
	clk.advance(stableAfter - time.Second)
	m.Watchdog(up())
	if m.retries[tun.Name] == nil {
		t.Fatal("count reset before the tunnel was stable")
	}
	clk.advance(time.Second)
	m.Watchdog(up())
	if m.retries[tun.Name] != nil {
		t.Error("count not reset after stable uptime")
	}
}

func TestWatchdogRespectsReconnectOff(t *testing.T) {
	m, tun := fakeManager(t, "fail")
	off := false
	tun.Reconnect = &off
	m.now = (&clock{t: time.Unix(1000, 0)}).now
	_ = m.Start(tun)
	eventually(t, "failure", func() bool { return row(m, tun).Status == Failed })
	for range 3 {
		if got := watch(m, tun); got != nil {
			t.Fatal("restarted although reconnect is off")
		}
	}
}

func TestKillAllEndsWatching(t *testing.T) {
	m, tun := fakeManager(t, "run")
	clk := &clock{t: time.Unix(1000, 0)}
	m.now = clk.now
	_ = m.Start(tun)
	r := row(m, tun)
	m.KillAll([]proc.Proc{{PID: r.PID}})
	eventually(t, "exit", func() bool { return row(m, tun).Status == Down })
	clk.advance(time.Hour)
	if got := watch(m, tun); got != nil {
		t.Errorf("Watchdog revived a killed tunnel: %v", got)
	}
}
