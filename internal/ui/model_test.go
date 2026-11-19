package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lennartjuette/sshhh/internal/config"
	"github.com/lennartjuette/sshhh/internal/proc"
	"github.com/lennartjuette/sshhh/internal/sshcmd"
	"github.com/lennartjuette/sshhh/internal/tunnel"
)

// With SSHHH_FAKE_SSH=run the test binary plays an ssh tunnel that runs
// until it is stopped, so the tests never touch real processes.
func TestMain(m *testing.M) {
	if os.Getenv("SSHHH_FAKE_SSH") == "run" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// newModel returns a sized model whose scans always return procs.
func newModel(t *testing.T, tunnels []config.Tunnel, procs ...proc.Proc) *Model {
	t.Helper()
	t.Setenv("SSHHH_FAKE_SSH", "run")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := New(tunnel.NewManager(exe, t.TempDir()), filepath.Join(t.TempDir(), "missing.toml"), "test")
	m.load = func() (config.Config, error) { return config.Config{Tunnels: tunnels}, nil }
	m.scan = func(context.Context) ([]proc.Proc, error) { return procs, nil }
	m.reload()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(m.scanCmd(false)())
	t.Cleanup(func() { m.mgr.StopAll(m.rows) })
	return m
}

func press(m *Model, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		default:
			r := []rune(k)[0]
			msg = tea.KeyPressMsg{Code: r, Text: k}
		}
		_, cmd = m.Update(msg)
	}
	return cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
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

var testTunnels = []config.Tunnel{
	{Name: "alpha", Host: "a", Socks: 1},
	{Name: "beta", Host: "b", Socks: 2},
	{Name: "gamma", Host: "c", Socks: 3},
}

func TestNavigation(t *testing.T) {
	m := newModel(t, testTunnels)
	press(m, "down", "down", "down", "down")
	if m.sel != 2 {
		t.Errorf("sel = %d after moving past the end; want 2", m.sel)
	}
	press(m, "up", "k", "k")
	if m.sel != 0 {
		t.Errorf("sel = %d after moving past the start; want 0", m.sel)
	}
	press(m, "j")
	if m.sel != 1 {
		t.Errorf("sel = %d after j; want 1", m.sel)
	}
}

func TestStartAndStopFromKeyboard(t *testing.T) {
	m := newModel(t, testTunnels)
	press(m, "down", "enter")
	r := m.rows[1]
	if r.Status != tunnel.Starting || r.PID == 0 {
		t.Fatalf("beta after enter: %+v", r)
	}
	press(m, "s")
	eventually(t, "stop", func() bool { m.refresh(); return m.rows[1].Status == tunnel.Down })
}

func TestQuitWithoutTunnelsQuits(t *testing.T) {
	m := newModel(t, testTunnels)
	if !isQuit(press(m, "q")) {
		t.Error("q with nothing running must quit right away")
	}
}

func TestQuitKeepsTunnels(t *testing.T) {
	m := newModel(t, testTunnels)
	press(m, "enter")
	pid := m.rows[0].PID
	if cmd := press(m, "q"); cmd != nil || m.prompt == nil {
		t.Fatal("q with a running tunnel must ask first")
	}
	if !isQuit(press(m, "enter")) { // default: keep running
		t.Fatal("enter must accept the default and quit")
	}
	if !proc.Exists(pid) {
		t.Error("tunnel was stopped although it should keep running")
	}
}

func TestQuitStopsTunnels(t *testing.T) {
	m := newModel(t, testTunnels)
	press(m, "enter")
	press(m, "q")
	if !isQuit(press(m, "n")) {
		t.Fatal("n must quit")
	}
	eventually(t, "tunnel exit", func() bool { m.refresh(); return m.rows[0].Status == tunnel.Down })
}

func TestEscCancelsPrompt(t *testing.T) {
	m := newModel(t, testTunnels)
	press(m, "enter", "q", "esc")
	if m.prompt != nil || m.rows[0].PID == 0 {
		t.Error("esc must close the prompt and change nothing")
	}
	press(m, "K", "x") // unrelated keys are ignored while asking
	if m.prompt != nil {
		t.Error("K without ssh processes must not ask")
	}
}

func TestKillAllAsksAndDefaultsToNo(t *testing.T) {
	procs := []proc.Proc{{PID: 999999, Dest: "x"}} // never terminated: the answer is no
	m := newModel(t, testTunnels, procs...)
	press(m, "K")
	if m.prompt == nil || !strings.Contains(m.prompt.text, "Terminate all 1 ssh") {
		t.Fatalf("prompt = %+v", m.prompt)
	}
	if cmd := press(m, "enter"); cmd != nil || m.prompt != nil {
		t.Error("enter must answer no and close the prompt")
	}
}

func TestAutostart(t *testing.T) {
	tunnels := []config.Tunnel{
		{Name: "auto", Host: "a", Socks: 1, Autostart: true},
		{Name: "manual", Host: "b", Socks: 2},
		{Name: "running", Host: "c", Socks: 3, Autostart: true},
	}
	running := proc.Proc{PID: 4242, Dest: "c", Forwards: []sshcmd.Forward{{Kind: sshcmd.Dynamic, Spec: "3"}}}
	m := newModel(t, tunnels, running)
	if m.rows[0].PID == 0 || m.rows[0].PID == 4242 {
		t.Errorf("auto was not started: %+v", m.rows[0])
	}
	if m.rows[1].PID != 0 {
		t.Error("manual must not start")
	}
	if m.rows[2].PID != 4242 {
		t.Errorf("running must be adopted, not started twice: %+v", m.rows[2])
	}
	if !strings.Contains(m.note.text, "auto") || strings.Contains(m.note.text, "running") {
		t.Errorf("note = %q", m.note.text)
	}
}

func TestView(t *testing.T) {
	external := proc.Proc{PID: 77, Dest: "me@elsewhere", Forwards: []sshcmd.Forward{{Kind: sshcmd.Local, Spec: "8080:db:5432"}}}
	m := newModel(t, testTunnels, external)
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"███", "TUNNEL", "alpha", "○ down", "elsewhere", "L:8080:db:5432", "Señor DadOps"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q", want)
		}
	}

	m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	if out := ansi.Strip(m.View().Content); strings.Contains(out, "███") || !strings.Contains(out, "sshhh") {
		t.Error("a small terminal must get the compact title instead of the logo")
	}
}

func TestBrokenConfigIsShownAndReloaded(t *testing.T) {
	m := newModel(t, nil)
	broken := errors.New("line 3: toml: expected value")
	m.load = func() (config.Config, error) { return config.Config{}, broken }
	m.reload()
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "line 3") || !strings.Contains(out, "Press e") {
		t.Errorf("config error not shown:\n%s", out)
	}

	m.load = func() (config.Config, error) { return config.Config{Tunnels: testTunnels}, nil }
	m.Update(editedMsg{})
	if m.configErr != nil || len(m.rows) != 3 || !strings.Contains(m.note.text, "3 tunnel(s)") {
		t.Errorf("after fixing: err %v, %d rows, note %q", m.configErr, len(m.rows), m.note.text)
	}

	m.load = func() (config.Config, error) { return config.Config{}, broken }
	m.Update(editedMsg{})
	if len(m.rows) != 3 {
		t.Error("a broken reload must keep the previous tunnels")
	}
}

func TestEmptyConfigHint(t *testing.T) {
	m := newModel(t, nil)
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "No tunnels yet. Press e") {
		t.Errorf("hint missing:\n%s", out)
	}
}

func TestEditorFromEnv(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code --wait")
	if args := editor("f.toml").Args; strings.Join(args, " ") != "code --wait f.toml" {
		t.Errorf("editor args = %v", args)
	}
	t.Setenv("PAGER", "")
	if args := pager("x.log").Args; args[len(args)-1] != "x.log" {
		t.Errorf("pager args = %v", args)
	}
}

func TestRuleAboveExternalTunnels(t *testing.T) {
	external := proc.Proc{PID: 77, Dest: "elsewhere", Forwards: []sshcmd.Forward{{Kind: sshcmd.Dynamic, Spec: "9"}}}
	lines := strings.Split(ansi.Strip(newModel(t, testTunnels, external).renderTable()), "\n")
	ext := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "elsewhere") })
	if ext < 2 || !strings.HasPrefix(lines[ext-1], "├") || !strings.Contains(lines[ext-2], "gamma") {
		t.Errorf("no rule between gamma and the external tunnel:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(strings.Join(lines, "\n"), "not in config") {
		t.Error("the label is gone, the rule alone separates")
	}
	if out := newModel(t, testTunnels).renderTable(); strings.Count(ansi.Strip(out), "├") != 1 {
		t.Error("without external tunnels there must be only the header rule")
	}
	if out := newModel(t, nil, external).renderTable(); strings.Count(ansi.Strip(out), "├") != 1 {
		t.Error("with only external tunnels the header rule is enough")
	}
}

func TestTableWidthIsStable(t *testing.T) {
	fwd := []sshcmd.Forward{{Kind: sshcmd.Dynamic, Spec: "1"}}
	width := func(procs ...proc.Proc) int { return lipgloss.Width(newModel(t, testTunnels, procs...).renderTable()) }
	down := width()
	starting := width(proc.Proc{PID: 12345, Dest: "a", Forwards: fwd, Listening: map[int]bool{}})
	up := width(proc.Proc{PID: 12345, Dest: "a", Forwards: fwd, Listening: map[int]bool{1: true}})
	if down != starting || starting != up {
		t.Errorf("table width changes with the status: down %d, starting %d, up %d", down, starting, up)
	}
}

func TestReconnectStatusText(t *testing.T) {
	now := time.Unix(1000, 0)
	r := tunnel.Row{Status: tunnel.Reconnecting, RetryAt: now.Add(3200 * time.Millisecond)}
	if got := statusText(r, 0, now); got != "↻ in 4s" {
		t.Errorf("statusText = %q; want ↻ in 4s", got)
	}
	r.RetryAt = time.Time{}
	if got := statusText(r, 0, now); got != "↻ now" {
		t.Errorf("statusText = %q; want ↻ now", got)
	}
}

func TestEnterCancelsReconnect(t *testing.T) {
	alpha := proc.Proc{PID: 4242, Dest: "a", Forwards: []sshcmd.Forward{{Kind: sshcmd.Dynamic, Spec: "1"}}, Listening: map[int]bool{1: true}}
	m := newModel(t, testTunnels, alpha)
	m.Update(frameMsg(time.Now())) // the watchdog sees alpha running
	m.scan = func(context.Context) ([]proc.Proc, error) { return nil, nil }
	m.Update(m.scanCmd(false)())   // alpha is gone
	m.Update(frameMsg(time.Now())) // the watchdog schedules a retry
	if m.rows[0].Status != tunnel.Reconnecting {
		t.Fatalf("alpha: %v; want reconnecting", m.rows[0].Status)
	}
	press(m, "enter")
	if m.rows[0].Status != tunnel.Down || !strings.Contains(m.note.text, "cancelled") {
		t.Errorf("after enter: %v, note %q", m.rows[0].Status, m.note.text)
	}
}

func TestExitText(t *testing.T) {
	for e, want := range map[tunnel.Exit]string{
		{Name: "a", Code: -1, LastLog: "x"}:         "a was killed",
		{Name: "a", Code: 255}:                      "a exited with code 255",
		{Name: "a", Code: 255, LastLog: "no route"}: "a exited with code 255: no route",
	} {
		if got := exitText(e); got != want {
			t.Errorf("exitText(%+v) = %q; want %q", e, got, want)
		}
	}
}

func TestAnimationSwitch(t *testing.T) {
	off := false
	m := newModel(t, testTunnels)
	m.load = func() (config.Config, error) { return config.Config{Animation: &off, Tunnels: testTunnels}, nil }
	m.reload()
	if m.animate {
		t.Fatal("animation = false in the config must stop the animation")
	}
	still := ansi.Strip(m.render())
	m.now = m.now.Add(3 * time.Second)
	if later := ansi.Strip(m.render()); stripClock(later) != stripClock(still) {
		t.Error("without animation the view must not change apart from the clock")
	}
	if !strings.Contains(still, taglines[0]) {
		t.Error("without animation the first tagline must stand still")
	}

	press(m, "a")
	if !m.animate || !strings.Contains(m.note.text, "animation on") {
		t.Errorf("a must turn the animation on: %v, %q", m.animate, m.note.text)
	}
	m.reload() // unchanged config: the key wins
	if !m.animate {
		t.Error("reloading an unchanged config must keep what the a key chose")
	}
	on := true
	m.load = func() (config.Config, error) { return config.Config{Animation: &on, Tunnels: testTunnels}, nil }
	press(m, "a")
	m.reload() // the setting changed in the file: the file wins
	if !m.animate {
		t.Error("a changed setting in the config must apply")
	}
}

// stripClock removes the time of day from the info line.
func stripClock(view string) string {
	return regexp.MustCompile(`\d\d:\d\d:\d\d`).ReplaceAllString(view, "")
}
