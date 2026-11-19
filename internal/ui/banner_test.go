package ui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

func TestGlyphsAreRectangular(t *testing.T) {
	for r, g := range glyphs {
		for _, row := range g {
			if utf8.RuneCountInString(row) != utf8.RuneCountInString(g[0]) {
				t.Errorf("glyph %q has rows of different width", r)
			}
		}
	}
	for _, r := range logo {
		if _, ok := glyphs[r]; !ok {
			t.Errorf("no glyph for %q", r)
		}
	}
}

func TestLogoKeepsItsShape(t *testing.T) {
	for _, tm := range []float64{0, 1.3, 47.9, 1e6} {
		lines := strings.Split(renderLogo(tm), "\n")
		if len(lines) != 6 {
			t.Fatalf("t=%v: %d lines", tm, len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != logoWidth {
				t.Errorf("t=%v line %d: width %d, want %d", tm, i, w, logoWidth)
			}
		}
	}
}

func TestTraffic(t *testing.T) {
	for _, tc := range []struct {
		active  bool
		clients int
	}{{false, 0}, {true, 0}, {true, 99}} {
		s := renderTraffic(12.34, 40, tc.active, tc.clients)
		if w := lipgloss.Width(s); w != 40 {
			t.Errorf("%+v: width %d", tc, w)
		}
		if moving := strings.Contains(s, "●"); moving != tc.active {
			t.Errorf("%+v: packets shown = %v", tc, moving)
		}
	}
	if renderTraffic(0, 0, true, 3) != "" {
		t.Error("zero width must render nothing")
	}
}

func TestTaglineTypesOut(t *testing.T) {
	first := taglines[0]
	if got := tagline(0); strings.TrimSpace(got) != "▌" {
		t.Errorf("t=0: %q", got)
	}
	if got := tagline(6.9); !strings.HasPrefix(got, first) {
		t.Errorf("t=6.9: %q", got)
	}
	if got := tagline(7.5); !strings.HasPrefix(taglines[1], strings.TrimRight(got, " ▌")) {
		t.Errorf("t=7.5 should start the second tagline: %q", got)
	}
	width := utf8.RuneCountInString(first) + 1
	for tm := 0.0; tm < 7; tm += 0.37 {
		if w := utf8.RuneCountInString(tagline(tm)); w != width {
			t.Fatalf("t=%v: width %d, want %d", tm, w, width)
		}
	}
}

func TestGradientLoops(t *testing.T) {
	if gradient(0) != gradient(1) || gradient(0.25) != gradient(-0.75) {
		t.Error("gradient must repeat every 1.0")
	}
	if gradient(0) != gradientStops[0] || gradient(0.5) != gradientStops[len(gradientStops)-1] {
		t.Error("gradient must run from the first stop to the last and back")
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[string]string{
		"42s": "42s", "5m12s": "5m12s", "3h5m": "3h05m", "52h": "2d04h",
	} {
		dur, _ := time.ParseDuration(d)
		if got := age(dur); got != want {
			t.Errorf("age(%s) = %q; want %q", d, got, want)
		}
	}
}
