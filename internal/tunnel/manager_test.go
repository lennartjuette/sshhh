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
	if lastLine(path+".missing") != "" {
		t.Error("missing file must give empty line")
	}
}
