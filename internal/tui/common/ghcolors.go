package common

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// GitHub's named colors (project options, issue types), for dark and light
// backgrounds.
var ghColors = map[string][2]string{
	"GRAY":   {"#9198a1", "#59636e"},
	"BLUE":   {"#4493f8", "#0969da"},
	"GREEN":  {"#3fb950", "#1a7f37"},
	"YELLOW": {"#d29922", "#9a6700"},
	"ORANGE": {"#db6d28", "#bc4c00"},
	"RED":    {"#f85149", "#d1242f"},
	"PINK":   {"#db61a2", "#bf3989"},
	"PURPLE": {"#ab7df8", "#8250df"},
}

// GhHex returns the hex value of a GitHub named color (GRAY when unknown).
func GhHex(name string, dark bool) string {
	c, ok := ghColors[strings.ToUpper(name)]
	if !ok {
		c = ghColors["GRAY"]
	}
	if dark {
		return c[0]
	}
	return c[1]
}

func GhColor(name string, dark bool) color.Color {
	return lipgloss.Color(GhHex(name, dark))
}
