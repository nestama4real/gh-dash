package boardsection

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/common"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/constants"
)

const (
	targetColumnWidth    = 36
	minColumnWidth       = 28
	collapsedColumnWidth = 18
	columnHeaderHeight   = 4 // title, 2 description lines, blank
)

func (m *Model) ghHex(name string) string {
	return common.GhHex(name, m.Ctx.HasDarkBackground)
}

func (m *Model) ghColor(name string) color.Color {
	return lipgloss.Color(m.ghHex(name))
}

// ghTint is the badge background: the option color blended into the page.
func (m *Model) ghTint(name string) color.Color {
	base := "#0d1117"
	alpha := 0.22
	if !m.Ctx.HasDarkBackground {
		base, alpha = "#ffffff", 0.16
	}
	return lipgloss.Color(blend(m.ghHex(name), base, alpha))
}

func blend(fg, bg string, alpha float64) string {
	parse := func(h string) (r, g, b float64) {
		v, _ := strconv.ParseUint(strings.TrimPrefix(h, "#"), 16, 32)
		return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff)
	}
	fr, fg_, fb := parse(fg)
	br, bg_, bb := parse(bg)
	mix := func(a, b float64) int { return int(a*alpha + b*(1-alpha)) }
	return fmt.Sprintf("#%02x%02x%02x", mix(fr, br), mix(fg_, bg_), mix(fb, bb))
}

func (m *Model) View() string {
	m.rebuild()
	d := m.GetDimensions()
	searchBar := m.SearchBar.View(m.Ctx)
	height := max(0, d.Height)
	board := m.renderBoard(d.Width, height)
	if m.editor != nil {
		board = m.overlayEditor(board, d.Width, height)
	}
	return m.Ctx.Styles.Section.ContainerStyle.
		Width(m.Ctx.MainContentWidth).
		Render(lipgloss.JoinVertical(lipgloss.Left, searchBar, board))
}

func (m *Model) placeholder(width, height int, text string) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, text)
}

func (m *Model) renderBoard(width, height int) string {
	if m.store.Project == nil {
		switch {
		case m.store.Err != nil:
			return m.placeholder(width, height, lipgloss.NewStyle().
				Foreground(m.Ctx.Theme.ErrorText).
				Render(fmt.Sprintf("Could not load the board: %v", m.store.Err)))
		default:
			return m.placeholder(width, height, lipgloss.NewStyle().
				Foreground(m.Ctx.Theme.FaintText).Render("Loading board…"))
		}
	}
	if len(m.columns) == 0 {
		return m.placeholder(width, height, "This project has no columns")
	}

	colWidth := max(minColumnWidth, width/max(1, width/targetColumnWidth))
	widthOf := func(c column) int {
		if c.collapsed {
			return collapsedColumnWidth
		}
		return colWidth
	}
	fits := func(from, to int) bool {
		w := 0
		for i := from; i <= to; i++ {
			w += widthOf(m.columns[i])
		}
		return w <= width
	}

	// Keep the selected column on screen.
	m.colOffset = clamp(m.colOffset, 0, len(m.columns)-1)
	if m.selCol < m.colOffset {
		m.colOffset = m.selCol
	}
	for m.colOffset < m.selCol && !fits(m.colOffset, m.selCol) {
		m.colOffset++
	}
	last := m.colOffset
	for last+1 < len(m.columns) && fits(m.colOffset, last+1) {
		last++
	}

	// One line for the scroll indicators.
	faint := lipgloss.NewStyle().Foreground(m.Ctx.Theme.FaintText)
	left, right := "", ""
	if m.colOffset > 0 {
		left = fmt.Sprintf("◀ %d more", m.colOffset)
	}
	if hidden := len(m.columns) - 1 - last; hidden > 0 {
		right = fmt.Sprintf("%d more ▶", hidden)
	}
	indicator := faint.Render(left) + strings.Repeat(" ",
		max(0, width-ansi.StringWidth(left)-ansi.StringWidth(right))) + faint.Render(right)

	cols := make([]string, 0, last-m.colOffset+1)
	used := 0
	for i := m.colOffset; i <= last; i++ {
		w := widthOf(m.columns[i])
		cols = append(cols, m.renderColumn(i, w, height-1))
		used += w
	}
	board := lipgloss.JoinHorizontal(lipgloss.Top, cols...)
	return lipgloss.JoinVertical(lipgloss.Left, indicator, board)
}

func (m *Model) renderColumn(idx, width, height int) string {
	c := &m.columns[idx]
	selected := idx == m.selCol
	accent := m.ghColor(c.color)

	borderColor := m.Ctx.Theme.FaintBorder
	if selected {
		borderColor = m.Ctx.Theme.SecondaryBorder
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(width).
		Height(height).
		MaxHeight(height).
		Padding(0, 1)
	inner := max(1, width-box.GetHorizontalFrameSize())
	innerHeight := max(0, height-box.GetVerticalFrameSize())

	title := lipgloss.NewStyle().Foreground(accent).Bold(true)
	count := lipgloss.NewStyle().
		Foreground(m.Ctx.Theme.FaintText).
		Render(fmt.Sprintf(" %d", len(c.items)))
	header := ansi.Truncate(
		title.Render("○ "+c.title)+count, inner, "…")

	if c.collapsed {
		faint := lipgloss.NewStyle().Foreground(m.Ctx.Theme.FaintText)
		return box.Render(lipgloss.JoinVertical(lipgloss.Left,
			ansi.Truncate(title.Render("○ "+c.title), inner, "…"),
			faint.Render(fmt.Sprintf("%d items", len(c.items))),
			"",
			faint.Render(strings.Join(wrapLines(c.description, inner, 3), "\n")),
		))
	}

	desc := wrapLines(c.description, inner, 2)
	for len(desc) < 2 {
		desc = append(desc, "")
	}
	descView := lipgloss.NewStyle().Foreground(m.Ctx.Theme.FaintText).
		Render(strings.Join(desc, "\n"))

	cardsHeight := max(0, innerHeight-columnHeaderHeight)
	cards := m.renderCards(c, selected, inner, cardsHeight)
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, header, descView, "", cards))
}

func (m *Model) renderCards(c *column, selected bool, width, height int) string {
	if len(c.items) == 0 || height <= 0 {
		return ""
	}
	sel := clamp(m.selRow[c.key], 0, len(c.items)-1)
	rendered := make([]string, len(c.items))
	heights := make([]int, len(c.items))
	for i, it := range c.items {
		rendered[i] = m.cachedCard(it, selected && i == sel, width)
		heights[i] = lipgloss.Height(rendered[i])
	}

	// Scroll so the selected card is fully visible (one line kept for "more").
	off := clamp(m.rowOffset[c.key], 0, len(c.items)-1)
	if sel < off {
		off = sel
	}
	for off < sel {
		h := 0
		for i := off; i <= sel; i++ {
			h += heights[i]
		}
		if off > 0 {
			h++
		}
		if h+1 <= height {
			break
		}
		off++
	}
	m.rowOffset[c.key] = off

	faint := lipgloss.NewStyle().Foreground(m.Ctx.Theme.FaintText)
	var out []string
	used := 0
	if off > 0 {
		out = append(out, faint.Render(fmt.Sprintf("  ⋮ %d above", off)))
		used++
	}
	i := off
	for ; i < len(c.items); i++ {
		remaining := len(c.items) - i - 1
		reserve := 0
		if remaining > 0 {
			reserve = 1
		}
		if used+heights[i]+reserve > height && i > off {
			break
		}
		out = append(out, rendered[i])
		used += heights[i]
	}
	if i < len(c.items) {
		out = append(out, faint.Render(fmt.Sprintf("  ⋮ %d more", len(c.items)-i)))
	}
	return lipgloss.NewStyle().MaxHeight(height).
		Render(lipgloss.JoinVertical(lipgloss.Left, out...))
}

// cachedCard renders a card, reusing it while the board and the width do
// not change.
func (m *Model) cachedCard(it *data.ProjectItem, selected bool, width int) string {
	key := fmt.Sprintf("%p|%d|%d|%v", m.store.Project, m.store.Version, width, m.Ctx.HasDarkBackground)
	if key != m.cardKey || m.cardCache == nil {
		m.cardCache, m.cardKey = map[string]string{}, key
	}
	ck := fmt.Sprintf("%s|%v", it.Id, selected)
	if c, ok := m.cardCache[ck]; ok {
		return c
	}
	c := m.renderCard(it, selected, width)
	m.cardCache[ck] = c
	return c
}

func (m *Model) renderCard(it *data.ProjectItem, selected bool, width int) string {
	theme := m.Ctx.Theme
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.FaintBorder).
		Width(width).
		Padding(0, 1)
	if selected {
		style = style.Border(lipgloss.ThickBorder()).
			BorderForeground(theme.PrimaryText)
	}
	inner := max(1, width-style.GetHorizontalFrameSize())
	faint := lipgloss.NewStyle().Foreground(theme.FaintText)

	// Line 1: state icon, repo#number, assignees.
	c := &it.Content
	var icon string
	switch {
	case it.ContentType == "DraftIssue":
		icon = faint.Render(constants.DraftIcon)
	case c.State == "OPEN":
		icon = lipgloss.NewStyle().Foreground(m.Ctx.Styles.Colors.OpenIssue).
			Render(constants.OpenIcon)
	case c.State == "MERGED":
		icon = lipgloss.NewStyle().Foreground(m.Ctx.Styles.Colors.MergedPR).
			Render(constants.MergedIcon)
	default:
		icon = lipgloss.NewStyle().Foreground(m.ghColor("PURPLE")).
			Render(constants.ClosedIcon)
	}
	ref := "Draft"
	if c.Number > 0 {
		ref = fmt.Sprintf("%s #%d", c.Repository.Name, c.Number)
	}
	line1 := icon + " " + faint.Render(ref)
	if m.isVisible("Assignees") && len(c.Assignees.Nodes) > 0 {
		logins := make([]string, 0, len(c.Assignees.Nodes))
		for _, a := range c.Assignees.Nodes {
			logins = append(logins, "@"+a.Login)
		}
		room := inner - ansi.StringWidth(line1) - 1
		if room > 4 {
			who := ansi.Truncate(strings.Join(logins, " "), room, "…")
			line1 += strings.Repeat(" ", max(1, inner-ansi.StringWidth(line1)-
				ansi.StringWidth(who))) + faint.Render(who)
		}
	}

	titleStyle := lipgloss.NewStyle().Foreground(theme.PrimaryText)
	if selected {
		titleStyle = titleStyle.Bold(true)
	}
	title := titleStyle.Render(strings.Join(wrapLines(c.Title, inner, 2), "\n"))

	lines := []string{line1, title}
	if badges := m.renderBadges(it, inner); badges != "" {
		lines = append(lines, badges)
	}
	return style.Render(strings.Join(lines, "\n"))
}

func (m *Model) isVisible(field string) bool {
	if len(m.view.VisibleFields) == 0 {
		return true
	}
	for _, f := range m.view.VisibleFields {
		if strings.EqualFold(f, field) {
			return true
		}
	}
	return false
}

// cardFields lists the fields shown as badges: the view's visible fields,
// plus Priority and the estimate which are always shown.
func (m *Model) cardFields() []data.ProjectField {
	project := m.store.Project
	statusName := m.Ctx.Config.ScrumBoard.GetStatusField()
	var fields []data.ProjectField
	seen := map[string]bool{}
	addField := func(f data.ProjectField) {
		if seen[f.Name] || strings.EqualFold(f.Name, statusName) || !f.IsEditable() {
			return
		}
		seen[f.Name] = true
		fields = append(fields, f)
	}
	names := m.view.VisibleFields
	if len(names) == 0 {
		for _, f := range project.Fields {
			names = append(names, f.Name)
		}
	}
	for _, n := range names {
		if f, ok := project.Field(n); ok {
			addField(f)
		}
	}
	for _, f := range project.Fields {
		if isPriorityField(f.Name) || isEstimateField(f.Name) {
			addField(f)
		}
	}
	return fields
}

func isPriorityField(name string) bool {
	return strings.Contains(normalizeName(name), "priority")
}

func isEstimateField(name string) bool {
	n := normalizeName(name)
	return strings.Contains(n, "estimat") || n == "eta"
}

func isActualField(name string) bool {
	return strings.Contains(normalizeName(name), "actual")
}

func (m *Model) badgeLabel(f data.ProjectField, v data.ProjectFieldValue) string {
	if v.Number == nil {
		return v.String()
	}
	n := normalizeName(f.Name)
	num := data.FormatProjectNumber(*v.Number)
	switch {
	case strings.Contains(n, "sprint"):
		return "S" + num
	case isEstimateField(f.Name):
		return "ETA " + num
	case isActualField(f.Name):
		return "Act " + num
	default:
		return f.Name + " " + num
	}
}

func (m *Model) renderBadges(it *data.ProjectItem, width int) string {
	var badges []string
	for _, f := range m.cardFields() {
		v, ok := it.FieldValues[f.Name]
		if !ok {
			continue
		}
		colorName := "GRAY"
		if f.DataType == "SINGLE_SELECT" {
			if o, ok := f.OptionByName(v.Name); ok {
				colorName = o.Color
			}
		}
		style := lipgloss.NewStyle().
			Foreground(m.ghColor(colorName)).
			Background(m.ghTint(colorName))
		if f.DataType == "SINGLE_SELECT" {
			style = style.Bold(true)
		}
		badges = append(badges, style.Render(" "+m.badgeLabel(f, v)+" "))
	}

	// Wrap badges onto as many lines as needed.
	var lines []string
	cur, curW := "", 0
	for _, b := range badges {
		w := ansi.StringWidth(b)
		if curW > 0 && curW+1+w > width {
			lines = append(lines, cur)
			cur, curW = "", 0
		}
		if curW > 0 {
			cur += " "
			curW++
		}
		cur += ansi.Truncate(b, width, "…")
		curW += min(w, width)
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

// wrapLines word-wraps s to width and keeps at most maxLines lines,
// ending with an ellipsis when truncated.
func wrapLines(s string, width, maxLines int) []string {
	if s == "" || width <= 0 {
		return nil
	}
	lines := strings.Split(ansi.Wordwrap(s, width, " -"), "\n")
	out := make([]string, 0, maxLines)
	for i, l := range lines {
		if i == maxLines {
			out[maxLines-1] = ansi.Truncate(out[maxLines-1], width-1, "") + "…"
			break
		}
		out = append(out, ansi.Truncate(l, width, "…"))
	}
	return out
}
