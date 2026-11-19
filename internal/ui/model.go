// Package ui is the terminal interface of sshhh, built on Bubble Tea.
package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/lennartjuette/sshhh/internal/config"
	"github.com/lennartjuette/sshhh/internal/proc"
	"github.com/lennartjuette/sshhh/internal/tunnel"
)

const (
	frameInterval = 80 * time.Millisecond
	scanInterval  = 2 * time.Second
	scanTimeout   = 5 * time.Second
	noteTTL       = 6 * time.Second
)

// Model is the Bubble Tea model of the tunnel list.
type Model struct {
	mgr        *tunnel.Manager
	configPath string
	version    string
	load       func() ([]config.Tunnel, error)
	scan       func(context.Context) ([]proc.Proc, error)

	tunnels   []config.Tunnel
	configErr error // shown until the config loads again

	procs   []proc.Proc
	rows    []tunnel.Row
	scanned bool // first scan is done and autostart has run
	sel     int
	started time.Time
	now     time.Time
	width   int
	height  int
	prompt  *prompt
	note    note
}

// prompt is a yes/no question that takes over the keyboard until answered.
type prompt struct {
	text    string
	def     bool           // answer for Enter
	yes, no func() tea.Cmd // nil means "do nothing"
}

type noteKind int

const (
	noteInfo noteKind = iota
	noteWarn
	noteError
)

// note is a short-lived message above the key bar.
type note struct {
	text string
	kind noteKind
	at   time.Time
}

type (
	frameMsg  time.Time
	rescanMsg struct{}
	scanMsg   struct {
		procs    []proc.Proc
		err      error
		periodic bool // part of the 2s scan loop, schedules the next scan
	}
	doneMsg struct { // result of a background action
		text string
		err  error
	}
	editedMsg struct{ err error } // the editor was closed
)

// New returns a model for the tunnels configured in configPath. A broken
// config does not stop it: the error is shown and can be fixed with "e".
func New(mgr *tunnel.Manager, configPath, version string) *Model {
	now := time.Now()
	m := &Model{
		mgr: mgr, configPath: configPath, version: version,
		load: func() ([]config.Tunnel, error) { return config.Load(configPath) },
		scan: proc.Snapshot, started: now, now: now,
	}
	m.reload()
	return m
}

// reload reads the config again. If that fails, the previous tunnels stay.
func (m *Model) reload() {
	tunnels, err := m.load()
	m.configErr = err
	if err == nil {
		m.tunnels = tunnels
	}
	m.refresh()
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(nextFrame(), m.scanCmd(true))
}

func nextFrame() tea.Cmd {
	return tea.Tick(frameInterval, func(t time.Time) tea.Msg { return frameMsg(t) })
}

func (m *Model) scanCmd(periodic bool) tea.Cmd {
	scan := m.scan
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		procs, err := scan(ctx)
		return scanMsg{procs: procs, err: err, periodic: periodic}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case frameMsg:
		m.now = time.Time(msg)
		for _, e := range m.mgr.Exits() {
			m.notify(noteError, "%s", exitText(e))
		}
		m.refresh()
		if !m.scanned { // the watchdog needs to know what runs
			return m, nextFrame()
		}
		if started := m.mgr.Watchdog(m.rows); len(started) > 0 {
			m.notify(noteWarn, "reconnecting %s", strings.Join(started, ", "))
			m.refresh()
			return m, tea.Batch(nextFrame(), m.scanCmd(false))
		}
		return m, nextFrame()
	case scanMsg:
		return m, m.onScan(msg)
	case rescanMsg:
		return m, m.scanCmd(true)
	case doneMsg:
		if msg.err != nil {
			m.notify(noteError, "%v", msg.err)
		} else if msg.text != "" {
			m.notify(noteInfo, "%s", msg.text)
		}
		return m, m.scanCmd(false)
	case editedMsg:
		if msg.err != nil {
			m.notify(noteError, "editor: %v", msg.err)
			return m, nil
		}
		m.reload()
		if m.configErr == nil {
			m.notify(noteInfo, "config reloaded: %d tunnel(s)", len(m.tunnels))
		}
		return m, m.scanCmd(false)
	case tea.KeyPressMsg:
		return m, m.onKey(msg.String())
	}
	return m, nil
}

func (m *Model) onScan(msg scanMsg) tea.Cmd {
	var cmds []tea.Cmd
	if msg.periodic {
		cmds = append(cmds, tea.Tick(scanInterval, func(time.Time) tea.Msg { return rescanMsg{} }))
	}
	if msg.err != nil {
		m.notify(noteWarn, "cannot read processes: %v", msg.err)
		return tea.Batch(cmds...)
	}
	m.procs = msg.procs
	m.refresh()
	if !m.scanned {
		m.scanned = true
		cmds = append(cmds, m.autostart())
	}
	return tea.Batch(cmds...)
}

// refresh recomputes the rows from the last scan and the manager's state.
func (m *Model) refresh() {
	m.rows = m.mgr.Rows(m.tunnels, m.procs)
	m.sel = max(0, min(m.sel, len(m.rows)-1))
}

func (m *Model) autostart() tea.Cmd {
	var started []string
	for _, r := range m.rows {
		if r.Config == nil || !r.Config.Autostart || r.PID != 0 {
			continue
		}
		if err := m.mgr.Start(*r.Config); err != nil {
			m.notify(noteError, "%v", err)
			continue
		}
		started = append(started, r.Name)
	}
	if len(started) == 0 {
		return nil
	}
	m.notify(noteInfo, "tucked in: %s", strings.Join(started, ", "))
	m.refresh()
	return m.scanCmd(false)
}

func (m *Model) onKey(key string) tea.Cmd {
	if key == "ctrl+c" {
		return tea.Quit // leaves all tunnels running
	}
	if m.prompt != nil {
		return m.answer(key)
	}
	row, ok := m.selected()
	switch key {
	case "up", "k":
		m.sel = max(0, m.sel-1)
	case "down", "j":
		m.sel = max(0, min(len(m.rows)-1, m.sel+1))
	case "enter", "space", "s":
		if ok {
			return m.toggle(row)
		}
	case "r":
		if ok {
			return m.restart(row)
		}
	case "l":
		if ok {
			return m.showLog(row)
		}
	case "e":
		return tea.ExecProcess(editor(m.configPath), func(err error) tea.Msg { return editedMsg{err} })
	case "K":
		m.confirmKillAll()
	case "q", "esc":
		return m.quit()
	}
	return nil
}

func (m *Model) selected() (tunnel.Row, bool) {
	if m.sel < len(m.rows) {
		return m.rows[m.sel], true
	}
	return tunnel.Row{}, false
}

func (m *Model) answer(key string) tea.Cmd {
	p := m.prompt
	var yes bool
	switch key {
	case "y", "Y", "j", "J":
		yes = true
	case "n", "N":
		yes = false
	case "enter":
		yes = p.def
	case "esc":
		m.prompt = nil
		return nil
	default:
		return nil
	}
	m.prompt = nil
	if action := map[bool]func() tea.Cmd{true: p.yes, false: p.no}[yes]; action != nil {
		return action()
	}
	return nil
}

func (m *Model) toggle(r tunnel.Row) tea.Cmd {
	switch {
	case r.Status == tunnel.Reconnecting && r.PID == 0:
		_ = m.mgr.Stop(r)
		m.notify(noteInfo, "%s: reconnect cancelled", r.Name)
	case r.PID != 0:
		if err := m.mgr.Stop(r); err != nil {
			m.notify(noteError, "stop %s: %v", r.Name, err)
		} else {
			m.notify(noteInfo, "%s stopped", r.Name)
		}
	case r.Config != nil:
		if err := m.mgr.Start(*r.Config); err != nil {
			m.notify(noteError, "%v", err)
		} else {
			m.notify(noteInfo, "%s tucked in", r.Name)
		}
	default:
		m.notify(noteWarn, "%s is not in the config, so there is nothing to start", r.Name)
	}
	m.refresh()
	return m.scanCmd(false)
}

func (m *Model) restart(r tunnel.Row) tea.Cmd {
	m.notify(noteInfo, "restarting %s …", r.Name)
	mgr := m.mgr
	return func() tea.Msg {
		return doneMsg{text: r.Name + " restarted", err: mgr.Restart(r)}
	}
}

func (m *Model) showLog(r tunnel.Row) tea.Cmd {
	if r.Config == nil {
		m.notify(noteWarn, "no log: %s was not started by sshhh", r.Name)
		return nil
	}
	path := m.mgr.LogPath(r.Name)
	if _, err := os.Stat(path); err != nil {
		m.notify(noteWarn, "no log yet for %s", r.Name)
		return nil
	}
	return tea.ExecProcess(pager(path), func(err error) tea.Msg { return doneMsg{err: err} })
}

func (m *Model) confirmKillAll() {
	procs := m.procs
	if len(procs) == 0 {
		m.notify(noteInfo, "no ssh processes running")
		return
	}
	m.prompt = &prompt{
		text: fmt.Sprintf("Terminate all %d ssh processes, interactive sessions included? [y/N]", len(procs)),
		yes: func() tea.Cmd {
			m.notify(noteWarn, "terminated %d ssh processes. kill -9 is not a parenting strategy.", m.mgr.KillAll(procs))
			return m.scanCmd(false)
		},
	}
}

// quit asks whether configured tunnels should keep running. Tunnels that are
// not in the config were started elsewhere and are left alone.
func (m *Model) quit() tea.Cmd {
	var running []tunnel.Row
	for _, r := range m.rows {
		if r.Config != nil && r.PID != 0 {
			running = append(running, r)
		}
	}
	if len(running) == 0 {
		return tea.Quit
	}
	m.prompt = &prompt{
		text: fmt.Sprintf("%d tunnel(s) running. Let them sleep in the background? [Y/n]", len(running)),
		def:  true,
		yes:  func() tea.Cmd { return tea.Quit },
		no: func() tea.Cmd {
			m.mgr.StopAll(running)
			return tea.Quit
		},
	}
	return nil
}

func exitText(e tunnel.Exit) string {
	switch {
	case e.Code < 0:
		return e.Name + " was killed"
	case e.LastLog == "":
		return fmt.Sprintf("%s exited with code %d", e.Name, e.Code)
	}
	return fmt.Sprintf("%s exited with code %d: %s", e.Name, e.Code, e.LastLog)
}

func (m *Model) notify(kind noteKind, format string, args ...any) {
	m.note = note{text: fmt.Sprintf(format, args...), kind: kind, at: m.now}
}
