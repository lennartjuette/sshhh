package ui

import (
	"math"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

const logo = "SSHHH"

// glyphs is an "ANSI Shadow" style font, limited to the letters of the logo.
var glyphs = map[rune][6]string{
	'S': {"███████╗", "██╔════╝", "███████╗", "╚════██║", "███████║", "╚══════╝"},
	'H': {"██╗  ██╗", "██║  ██║", "███████║", "██╔══██║", "██║  ██║", "╚═╝  ╚═╝"},
}

// logoWidth is the width of the logo in terminal cells.
var logoWidth = func() int {
	w := 0
	for _, r := range logo {
		w += utf8.RuneCountInString(glyphs[r][0])
	}
	return w
}()

// whisper makes each letter a little quieter than the one before: SSHhh…
var whisper = [len(logo)]float64{1, 1, 0.9, 0.72, 0.52}

// taglines rotate below the logo.
var taglines = []string{
	"shhh… the tunnels are sleeping in the background",
	"4 kids · n tunnels · 0 sleep",
	"ssh -N: children who never execute a command",
	"they move out (setsid) but never stop running",
	"kill -9 is not a parenting strategy",
	"are we there yet? port 1080 is.",
	"forked four times, still managing child processes",
}

// renderLogo draws the logo at animation time t (seconds): a color gradient
// drifting across the letters, a shine sweeping over them every few seconds
// and drop shadows in a darker shade of the same color.
func renderLogo(t float64) string {
	var b strings.Builder
	shine := math.Mod(t*28, float64(logoWidth)+70) - 35
	for y := range 6 {
		x := 0
		for i, letter := range logo {
			for _, ch := range glyphs[letter][y] {
				if ch == ' ' {
					b.WriteByte(' ')
					x++
					continue
				}
				c := gradient((float64(x)/2 + float64(y)*1.5 - t*14) / 48).scale(whisper[i])
				switch {
				case ch != '█':
					c = c.scale(0.45)
				case math.Abs(float64(x-2*y)-shine) < 2.5:
					c = white.scale(whisper[i])
				}
				b.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(ch)))
				x++
			}
		}
		if y < 5 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderTraffic draws a line of the given width with packets flowing through
// it, more and faster the more clients use the tunnels. Nothing moves while
// no tunnel runs.
func renderTraffic(t float64, width int, active bool, clients int) string {
	cells := make([]string, width)
	line := lipgloss.NewStyle().Foreground(colorFaint).Render("─")
	for i := range cells {
		cells[i] = line
	}
	if active && width > 0 {
		load := min(clients, 10)
		n, speed := 2+load, 12+4*float64(load)
		gap := float64(width) / float64(n)
		for i := range n {
			back := wrap(int(-t*speed*0.7+(float64(i)+0.5)*gap), width)
			fwd := wrap(int(t*speed+float64(i)*gap), width)
			cells[back] = lipgloss.NewStyle().Foreground(gradient(float64(back) / float64(width)).scale(0.6)).Render("•")
			cells[fwd] = lipgloss.NewStyle().Foreground(gradient(float64(fwd) / float64(width))).Bold(true).Render("●")
		}
	}
	return strings.Join(cells, "")
}

// tagline types out one tagline after another. The result always has the
// same width for a given tagline, so centering it does not wobble.
func tagline(t float64) string {
	const period = 7.0
	text := []rune(taglines[int(t/period)%len(taglines)])
	n := min(len(text), int(math.Mod(t, period)*30))
	cursor := " "
	if n < len(text) || math.Mod(t, 1) < 0.5 {
		cursor = "▌"
	}
	return string(text[:n]) + cursor + strings.Repeat(" ", len(text)-n)
}

func wrap(i, n int) int { return (i%n + n) % n }
