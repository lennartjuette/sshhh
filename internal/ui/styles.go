package ui

import (
	"image/color"
	"math"

	"charm.land/lipgloss/v2"
)

// rgb is a color that can be blended and dimmed. It implements color.Color,
// so it can be passed to lipgloss directly. Bubble Tea downsamples it for
// terminals without true color.
type rgb struct{ r, g, b float64 }

func (c rgb) RGBA() (r, g, b, a uint32) {
	return color.RGBA{uint8(c.r), uint8(c.g), uint8(c.b), 0xff}.RGBA()
}

func (c rgb) scale(f float64) rgb { return rgb{c.r * f, c.g * f, c.b * f} }

func mix(a, b rgb, w float64) rgb {
	return rgb{a.r + (b.r-a.r)*w, a.g + (b.g-a.g)*w, a.b + (b.b-a.b)*w}
}

var (
	white      = rgb{0xff, 0xff, 0xff}
	colorUp    = rgb{0x5f, 0xff, 0x87}
	colorUpDim = rgb{0x2e, 0x9e, 0x5b}

	colorAccent   = lipgloss.Color("#ff5fd7")
	colorMuted    = lipgloss.Color("#9a9aae")
	colorFaint    = lipgloss.Color("#4e4e62")
	colorSelected = lipgloss.Color("#2d2545")
	colorStarting = lipgloss.Color("#ffd75f")
	colorFailed   = lipgloss.Color("#ff5f5f")
	colorRetry    = lipgloss.Color("#ffaf5f")
	colorInk      = lipgloss.Color("#1a1a24")
)

// gradientStops run from cyan over violet and pink to orange.
var gradientStops = []rgb{
	{0x00, 0xe5, 0xff},
	{0x44, 0x8a, 0xff},
	{0xb3, 0x88, 0xff},
	{0xff, 0x40, 0x81},
	{0xff, 0xab, 0x40},
}

// gradient returns the color at position p on a loop through the stops and
// back. Any p works; the pattern repeats every 1.0.
func gradient(p float64) rgb {
	p -= math.Floor(p)
	f := (1 - math.Abs(2*p-1)) * float64(len(gradientStops)-1)
	i := min(int(f), len(gradientStops)-2)
	return mix(gradientStops[i], gradientStops[i+1], f-float64(i))
}

var (
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleFaint  = lipgloss.NewStyle().Foreground(colorFaint)
	styleHeader = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Padding(0, 1)
	styleCell   = lipgloss.NewStyle().Padding(0, 1)
	styleKey    = lipgloss.NewStyle().Foreground(colorInk).Background(colorAccent).Bold(true).Padding(0, 1)
	stylePrompt = lipgloss.NewStyle().Foreground(colorInk).Background(colorStarting).Bold(true).Padding(0, 1)
	styleTitle  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
)
