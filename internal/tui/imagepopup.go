package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/prrow"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/images"
)

// imagePopup shows the images of the selected issue or PR one at a time,
// large, over the dashboard.
type imagePopup struct {
	refs  []images.Ref
	index int
	view  string // rendered in Update: rendering may queue terminal sequences
}

var (
	popupPrev  = key.NewBinding(key.WithKeys("left", "h", "shift+tab", "p"))
	popupNext  = key.NewBinding(key.WithKeys("right", "l", "tab", "n", "space"))
	popupFirst = key.NewBinding(key.WithKeys("g", "home"))
	popupLast  = key.NewBinding(key.WithKeys("G", "end"))
	popupClose = key.NewBinding(key.WithKeys("esc", "q", "i", "enter"))
)

func rowImages(row data.RowData) []images.Ref {
	var bodies []string
	switch r := row.(type) {
	case *data.IssueData:
		if r != nil {
			bodies = append(bodies, r.Body)
			for _, c := range r.Comments.Nodes {
				bodies = append(bodies, c.Body)
			}
		}
	case *prrow.Data:
		if r != nil && r.IsEnriched {
			bodies = append(bodies, r.Enriched.Body)
			for _, c := range r.Enriched.Comments.Nodes {
				bodies = append(bodies, c.Body)
			}
		}
	}
	var refs []images.Ref
	seen := map[string]bool{}
	for _, b := range bodies {
		for _, ref := range images.FindImages(b) {
			if !seen[ref.Url] {
				seen[ref.Url] = true
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// viewImages opens the image popup on the selected issue or PR.
func (m *Model) viewImages(row data.RowData) tea.Cmd {
	refs := rowImages(row)
	if len(refs) == 0 {
		if pr, ok := row.(*prrow.Data); ok && pr != nil && !pr.IsEnriched {
			return m.notifyErr("The PR is still loading, try again in a moment")
		}
		return m.notifyErr("No image in this description")
	}
	if !images.Supported() {
		return m.notifyErr("Images need a terminal supporting kitty's graphics protocol")
	}
	m.imagePopup = &imagePopup{refs: refs}
	m.renderImagePopup()
	return nil
}

func (m *Model) updateImagePopup(msg tea.KeyMsg) {
	p := m.imagePopup
	switch {
	case key.Matches(msg, popupClose):
		m.imagePopup = nil
		return
	case key.Matches(msg, popupPrev):
		p.index = max(0, p.index-1)
	case key.Matches(msg, popupNext):
		p.index = min(len(p.refs)-1, p.index+1)
	case key.Matches(msg, popupFirst):
		p.index = 0
	case key.Matches(msg, popupLast):
		p.index = len(p.refs) - 1
	}
	m.renderImagePopup()
}

func (m *Model) popupSize() (int, int) {
	w := max(20, m.ctx.ScreenWidth*9/10)
	h := max(8, m.ctx.ScreenHeight*9/10)
	return min(w, m.ctx.ScreenWidth), min(h, m.ctx.ScreenHeight)
}

func (m *Model) renderImagePopup() {
	p := m.imagePopup
	if p == nil {
		return
	}
	width, height := m.popupSize()
	theme := m.ctx.Theme
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.PrimaryText).
		Padding(0, 1)
	innerW := max(1, width-box.GetHorizontalFrameSize())
	innerH := max(1, height-box.GetVerticalFrameSize())

	ref := p.refs[p.index]
	faint := lipgloss.NewStyle().Foreground(theme.FaintText)
	title := lipgloss.NewStyle().Bold(true).Foreground(theme.PrimaryText).
		Render(fmt.Sprintf("Image %d/%d", p.index+1, len(p.refs)))
	if ref.Alt != "" && !strings.EqualFold(ref.Alt, "image") {
		title += faint.Render("  " + ref.Alt)
	}
	hint := faint.Render("←/→ h/l previous/next • g/G first/last • esc close")

	imgH := max(1, innerH-4) // title, blank, blank, hint
	lines := images.PopupBlock(ref, innerW, imgH)
	// Center the image.
	imgW := 0
	for _, l := range lines {
		imgW = max(imgW, ansi.StringWidth(l))
	}
	left := strings.Repeat(" ", max(0, (innerW-imgW)/2))
	top := max(0, (imgH-len(lines))/2)
	body := make([]string, 0, imgH)
	for i := 0; i < top; i++ {
		body = append(body, "")
	}
	for _, l := range lines {
		body = append(body, left+l)
	}
	for len(body) < imgH {
		body = append(body, "")
	}

	content := strings.Join(append([]string{ansi.Truncate(title, innerW, "…"), ""},
		append(body, "", ansi.Truncate(hint, innerW, "…"))...), "\n")
	p.view = box.Width(width).Height(height).Render(content)
}

// imagePopupLayer places the popup over the dashboard.
func (m *Model) imagePopupLayer() *lipgloss.Layer {
	if m.imagePopup == nil || m.imagePopup.view == "" {
		return nil
	}
	w, h := m.popupSize()
	return lipgloss.NewLayer(m.imagePopup.view).
		X(max(0, (m.ctx.ScreenWidth-w)/2)).
		Y(max(0, (m.ctx.ScreenHeight-h)/2)).
		Z(10)
}
