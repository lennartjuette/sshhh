package ui

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/lennartjuette/sshhh/internal/tunnel"
)

var spinner = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// Table columns.
const (
	colMarker = iota
	colName
	colStatus
	colPID
	colUptime
	colConn
	colForwards
	colTarget
)

var headers = []string{"", "TUNNEL", "STATUS", "PID", "UPTIME", "CONN", "FORWARDS", "TARGET"}

func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "sshhh"
	return v
}

func (m *Model) render() string {
	if m.width == 0 {
		return "" // wait for the first WindowSizeMsg
	}
	t := m.now.Sub(m.started).Seconds()
	center := func(s string) string { return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, s) }

	var parts []string
	if m.width >= logoWidth+4 && m.height >= len(m.rows)+20 {
		running, clients := m.traffic()
		parts = append(parts, "",
			center(renderLogo(t)),
			center(renderTraffic(t, logoWidth, running > 0, clients)),
			center(styleMuted.Italic(true).Render(tagline(t))),
			"")
	} else {
		parts = append(parts, styleTitle.Render(" sshhh"), "")
	}
	parts = append(parts, center(lipgloss.JoinVertical(lipgloss.Left, m.renderInfo(), m.renderTable())))

	body := lipgloss.JoinVertical(lipgloss.Left, parts...)
	footer := lipgloss.JoinVertical(lipgloss.Left, m.renderNote(), m.renderKeys())
	gap := max(1, m.height-lipgloss.Height(body)-lipgloss.Height(footer)+1)
	return body + strings.Repeat("\n", gap) + footer
}

// traffic returns the number of running tunnels and their total clients.
func (m *Model) traffic() (running, clients int) {
	for _, r := range m.rows {
		if r.Proc != nil {
			running++
			clients += r.Proc.Clients
		}
	}
	return running, clients
}

func (m *Model) renderInfo() string {
	running, _ := m.traffic()
	return styleMuted.Render(fmt.Sprintf(" %s · %d/%d tucked in · %s",
		shortenHome(m.configPath), running, len(m.rows), m.now.Format("15:04:05")))
}

func (m *Model) renderTable() string {
	var problem string
	if m.configErr != nil {
		problem = lipgloss.NewStyle().Foreground(colorFailed).Render(" ✖ "+m.configErr.Error()) +
			styleMuted.Render("\n   Press e to fix it.") + "\n"
	}
	if len(m.rows) == 0 {
		if problem != "" {
			return "\n" + problem
		}
		return styleMuted.Render("\n No tunnels yet. Press e to add your first one.")
	}
	t := m.now.Sub(m.started).Seconds()
	data := make([][]string, len(m.rows))
	for i, r := range m.rows {
		data[i] = m.cells(r, i == m.sel, t)
	}
	tbl := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleFaint).
		Headers(headers...).
		Rows(data...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return styleHeader
			}
			s := styleCell
			if row == m.sel {
				s = s.Background(colorSelected).Bold(true)
			}
			switch col {
			case colStatus:
				s = s.Foreground(statusColor(m.rows[row].Status, t))
			case colPID, colUptime, colConn:
				s = s.Align(lipgloss.Right)
			case colMarker:
				s = s.Foreground(colorAccent)
			case colForwards, colTarget:
				if row != m.sel {
					s = s.Foreground(colorMuted)
				}
			}
			return s
		})
	if lipgloss.Width(tbl.String()) > m.width {
		tbl.Width(m.width).Wrap(false)
	}
	return problem + tbl.String()
}

func (m *Model) cells(r tunnel.Row, selected bool, t float64) []string {
	c := make([]string, len(headers))
	if selected {
		c[colMarker] = "▸"
	}
	c[colName] = r.Name
	if r.Config == nil {
		c[colName] = "↳ " + r.Name
	} else if r.Config.Autostart {
		c[colName] += " ↻"
	}
	c[colStatus] = statusText(r, t)
	c[colPID], c[colUptime], c[colConn] = "–", "–", "–"
	if r.PID != 0 {
		c[colPID] = strconv.Itoa(int(r.PID))
	}
	var fwds []string
	if p := r.Proc; p != nil {
		if !p.Started.IsZero() {
			c[colUptime] = age(m.now.Sub(p.Started))
		}
		if p.Listening != nil {
			c[colConn] = strconv.Itoa(p.Clients)
		}
		for _, f := range p.Forwards {
			fwds = append(fwds, f.String())
		}
		c[colTarget] = p.Dest
	} else {
		spec := r.Config.Spec() // rows without process always have a config
		for _, f := range spec.Forwards {
			fwds = append(fwds, f.String())
		}
		c[colTarget] = spec.Destination()
	}
	c[colForwards] = strings.Join(fwds, " ")
	return c
}

func statusText(r tunnel.Row, t float64) string {
	switch r.Status {
	case tunnel.Up:
		return "● up"
	case tunnel.Starting:
		return string(spinner[int(t*12)%len(spinner)]) + " starting"
	case tunnel.Failed:
		if r.ExitCode < 0 {
			return "✖ killed"
		}
		return fmt.Sprintf("✖ exit %d", r.ExitCode)
	}
	return "○ down"
}

// statusColor returns the color of a status. Running tunnels breathe.
func statusColor(s tunnel.Status, t float64) color.Color {
	switch s {
	case tunnel.Up:
		return mix(colorUp, colorUpDim, (math.Sin(t*2.5)+1)/2)
	case tunnel.Starting:
		return colorStarting
	case tunnel.Failed:
		return colorFailed
	}
	return colorMuted
}

func (m *Model) renderNote() string {
	if m.prompt != nil {
		return " " + stylePrompt.Render(m.prompt.text)
	}
	if m.note.text == "" || m.now.Sub(m.note.at) > noteTTL {
		return ""
	}
	color := map[noteKind]lipgloss.Style{
		noteInfo:  styleMuted,
		noteWarn:  lipgloss.NewStyle().Foreground(colorStarting),
		noteError: lipgloss.NewStyle().Foreground(colorFailed),
	}[m.note.kind]
	return color.Render(" " + m.note.text)
}

func (m *Model) renderKeys() string {
	keys := [][2]string{{"↑↓", "select"}, {"⏎", "start/stop"}, {"r", "restart"}, {"l", "log"}, {"e", "edit config"}, {"K", "kill all"}, {"q", "quit"}}
	var b strings.Builder
	b.WriteString(" ")
	for _, k := range keys {
		b.WriteString(styleKey.Render(k[0]) + styleMuted.Render(" "+k[1]+"  "))
	}
	left := b.String()
	for _, sig := range []string{"sshhh " + m.version + " · by Señor DadOps ", "by Señor DadOps "} {
		if gap := m.width - lipgloss.Width(left) - lipgloss.Width(sig); gap > 0 {
			return left + strings.Repeat(" ", gap) + styleFaint.Render(sig)
		}
	}
	return left
}

// age formats a duration compactly: 42s, 5m12s, 3h05m, 2d04h.
func age(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
}

func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return path
}
