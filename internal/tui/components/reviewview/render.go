package reviewview

import (
	"fmt"
	"image/color"
	"path"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/common"
)

const (
	headerHeight = 3 // title, file, rule
	hintHeight   = 1
	gutterWidth  = 12 // "●1234 1234 +": comment mark, old and new numbers, +/-
)

func (m *Model) diffHeight() int {
	return max(1, m.height-headerHeight-hintHeight)
}

func (m *Model) treeWidth() int {
	if !m.showTree || m.width < 100 {
		return 0
	}
	return min(42, m.width/4)
}

func (m *Model) diffWidth() int {
	tw := m.treeWidth()
	if tw == 0 {
		return m.width
	}
	return m.width - tw - 1
}

func (m *Model) gh(name string) color.Color {
	return common.GhColor(name, m.ctx.HasDarkBackground)
}

func (m *Model) tint(name string, alpha float64) color.Color {
	base := "#0d1117"
	if !m.ctx.HasDarkBackground {
		base = "#ffffff"
	}
	return lipgloss.Color(blend(common.GhHex(name, m.ctx.HasDarkBackground), base, alpha))
}

func blend(fg, bg string, alpha float64) string {
	parse := func(h string) (float64, float64, float64) {
		v, _ := strconv.ParseUint(strings.TrimPrefix(h, "#"), 16, 32)
		return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff)
	}
	r1, g1, b1 := parse(fg)
	r2, g2, b2 := parse(bg)
	mix := func(a, b float64) int { return int(a*alpha + b*(1-alpha)) }
	return fmt.Sprintf("#%02x%02x%02x", mix(r1, r2), mix(g1, g2), mix(b1, b2))
}

func expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "    ")
}

func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	faint := lipgloss.NewStyle().Foreground(m.ctx.Theme.FaintText)
	switch {
	case m.loading:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			faint.Render(fmt.Sprintf("Loading the diff of #%d…", m.pr.Number)))
	case m.err != nil:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			lipgloss.NewStyle().Foreground(m.ctx.Theme.ErrorText).
				Render("Could not load the pull request: "+m.err.Error()+"\n\n")+
				faint.Render("q to go back"))
	case len(m.files) == 0:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			faint.Render("No changed file. q to go back"))
	}

	m.clampOffset()
	body := m.renderDiff()
	if tw := m.treeWidth(); tw > 0 {
		sep := lipgloss.NewStyle().Foreground(m.ctx.Theme.FaintBorder).
			Render(strings.TrimSuffix(strings.Repeat("│\n", m.diffHeight()), "\n"))
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.renderTree(tw), sep, body)
	}
	screen := lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderHint())

	if dialog := m.renderDialog(); dialog != "" {
		x := max(0, (m.width-lipgloss.Width(dialog))/2)
		y := max(0, (m.height-lipgloss.Height(dialog))/2)
		screen = lipgloss.NewCompositor(
			lipgloss.NewLayer(screen),
			lipgloss.NewLayer(dialog).X(x).Y(y).Z(1),
		).Render()
	}
	return screen
}

func (m *Model) renderHeader() string {
	theme := m.ctx.Theme
	title := lipgloss.NewStyle().Bold(true).Foreground(theme.PrimaryText).
		Render(fmt.Sprintf(" Review #%d ", m.pr.Number)) +
		lipgloss.NewStyle().Foreground(theme.SecondaryText).Render(m.pr.Title)
	right := ""
	if n := len(m.pending()); n > 0 {
		right = lipgloss.NewStyle().Foreground(m.gh("YELLOW")).Bold(true).
			Render(fmt.Sprintf("%d pending comment%s ", n, plural(n))) +
			lipgloss.NewStyle().Foreground(theme.FaintText).Render("· S to submit ")
	}
	line1 := ansi.Truncate(title, max(1, m.width-ansi.StringWidth(right)-1), "…")
	line1 += strings.Repeat(" ", max(1, m.width-ansi.StringWidth(line1)-ansi.StringWidth(right))) + right

	f := m.currFile()
	name := f.Filename
	if f.PreviousFilename != "" && f.PreviousFilename != f.Filename {
		name = f.PreviousFilename + " → " + f.Filename
	}
	line2 := " " + lipgloss.NewStyle().Bold(true).Foreground(theme.PrimaryText).Render(name) + "  " +
		lipgloss.NewStyle().Foreground(m.gh("GREEN")).Render(fmt.Sprintf("+%d", f.Additions)) + " " +
		lipgloss.NewStyle().Foreground(m.gh("RED")).Render(fmt.Sprintf("-%d", f.Deletions)) +
		lipgloss.NewStyle().Foreground(theme.FaintText).
			Render(fmt.Sprintf("   file %d/%d", m.fileIdx+1, len(m.files)))
	rule := lipgloss.NewStyle().Foreground(theme.FaintBorder).Render(strings.Repeat("─", m.width))
	return lipgloss.JoinVertical(lipgloss.Left, line1, ansi.Truncate(line2, m.width, "…"), rule)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (m *Model) renderTree(width int) string {
	theme := m.ctx.Theme
	height := m.diffHeight()
	start := 0
	if m.fileIdx >= height {
		start = m.fileIdx - height + 1
	}
	pendingPerFile := map[string]int{}
	for _, d := range m.pending() {
		pendingPerFile[d.Path]++
	}
	lines := make([]string, 0, height)
	for i := start; i < len(m.files) && len(lines) < height; i++ {
		f := m.files[i]
		var mark string
		switch f.Status {
		case "added":
			mark = lipgloss.NewStyle().Foreground(m.gh("GREEN")).Render("A")
		case "removed":
			mark = lipgloss.NewStyle().Foreground(m.gh("RED")).Render("D")
		case "renamed":
			mark = lipgloss.NewStyle().Foreground(m.gh("BLUE")).Render("R")
		default:
			mark = lipgloss.NewStyle().Foreground(m.gh("YELLOW")).Render("M")
		}
		extra := ""
		if n := pendingPerFile[f.Filename]; n > 0 {
			extra = lipgloss.NewStyle().Foreground(m.gh("YELLOW")).Render(fmt.Sprintf(" ●%d", n))
		}
		dir, base := path.Split(f.Filename)
		nameW := max(4, width-3-ansi.StringWidth(extra))
		name := base
		if room := nameW - ansi.StringWidth(base) - 1; room > 4 && dir != "" {
			name = lipgloss.NewStyle().Foreground(theme.FaintText).
				Render(ansi.TruncateLeft(dir, max(0, ansi.StringWidth(dir)-room), "…")) + base
		}
		row := " " + mark + " " + ansi.Truncate(name, nameW, "…") + extra
		style := lipgloss.NewStyle().Width(width).MaxWidth(width)
		if i == m.fileIdx {
			style = style.Background(theme.SelectedBackground).Bold(true)
		}
		lines = append(lines, style.Render(row))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// lineRows is the number of screen rows of a diff line once wrapped.
func (m *Model) lineRows(l diffLine) int {
	avail := max(1, m.diffWidth()-gutterWidth)
	w := ansi.StringWidth(expandTabs(l.text))
	return max(1, (w+avail-1)/avail)
}

// blocks are the comments shown under a line: existing threads and pending
// comments.
func (m *Model) blocks(f *fileDiff, i int) []string {
	l := f.lines[i]
	if !l.commentable() {
		return nil
	}
	side, no := l.position()
	width := max(20, min(100, m.diffWidth()-gutterWidth-1))
	var out []string
	for _, t := range m.threads[f.Filename] {
		tSide := t.DiffSide
		if tSide == "" {
			tSide = "RIGHT"
		}
		if t.Line == no && tSide == side {
			out = append(out, m.renderThread(t, width))
		}
	}
	for _, d := range m.pending() {
		if d.Path == f.Filename && d.Side == side && d.Line == no {
			out = append(out, m.renderDraft(d, width))
		}
	}
	return out
}

func (m *Model) indentBlock(s string) []string {
	pad := strings.Repeat(" ", gutterWidth)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return lines
}

// commentBox renders comments in a box tinted with the given color, so that
// they stand out from the code. Every piece carries the background: inner
// styles would otherwise reset it.
func (m *Model) commentBox(colorName, title string, sections [][2]string, width int) string {
	theme := m.ctx.Theme
	bg := m.tint(colorName, 0.13)
	accent := m.gh(colorName)
	inner := max(10, width-4)
	line := func(s lipgloss.Style, text string) string {
		return s.Background(bg).Width(inner).Render(text)
	}
	rows := []string{line(lipgloss.NewStyle().Foreground(accent).Bold(true), title)}
	for _, sec := range sections {
		rows = append(rows, line(lipgloss.NewStyle(), ""))
		if sec[0] != "" {
			rows = append(rows, line(lipgloss.NewStyle().Foreground(accent).Bold(true), sec[0]))
		}
		rows = append(rows, line(lipgloss.NewStyle().Foreground(theme.PrimaryText), sec[1]))
	}
	return lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), false, false, false, true).
		BorderForeground(accent).
		BorderBackground(bg).
		Background(bg).
		Padding(0, 1).
		Width(width).
		Render(strings.Join(rows, "\n"))
}

func (m *Model) renderThread(t data.PRReviewThread, width int) string {
	var sections [][2]string
	for _, c := range t.Comments {
		sections = append(sections, [2]string{"@" + c.Author, strings.TrimSpace(c.Body)})
	}
	title := "💬 Conversation"
	colorName := "BLUE"
	if t.IsResolved {
		title += " · resolved"
		colorName = "GRAY"
	}
	if n := len(t.Comments); n > 1 {
		title += fmt.Sprintf(" · %d comments", n)
	}
	return m.commentBox(colorName, title, sections, width)
}

func (m *Model) renderDraft(d draft, width int) string {
	label := "✎ Pending comment"
	if d.StartLine > 0 && d.StartLine != d.Line {
		label = fmt.Sprintf("✎ Pending comment on lines %d-%d", d.StartLine, d.Line)
	}
	return m.commentBox("YELLOW", label+"  ·  c edit · x delete",
		[][2]string{{"", d.Body}}, width)
}

// hasComments reports whether comments are shown under line i, and their
// color.
func (m *Model) commentMark(f *fileDiff, i int) (string, bool) {
	l := f.lines[i]
	if !l.commentable() {
		return "", false
	}
	side, no := l.position()
	for _, d := range m.pending() {
		if d.Path == f.Filename && d.Side == side && d.Line == no {
			return "YELLOW", true
		}
	}
	for _, t := range m.threads[f.Filename] {
		tSide := t.DiffSide
		if tSide == "" {
			tSide = "RIGHT"
		}
		if t.Line == no && tSide == side {
			if t.IsResolved {
				return "GRAY", true
			}
			return "BLUE", true
		}
	}
	return "", false
}

// itemRows returns, for each line, its rows and the rows of its blocks.
func (m *Model) itemRows(f *fileDiff, i int) (int, []string) {
	rows := m.lineRows(f.lines[i])
	var blockLines []string
	for _, b := range m.blocks(f, i) {
		blockLines = append(blockLines, m.indentBlock(b)...)
	}
	return rows, blockLines
}

// clampOffset scrolls so that the cursor line is visible.
func (m *Model) clampOffset() {
	f := m.currFile()
	if f == nil || len(f.lines) == 0 {
		return
	}
	cur := m.currLine()
	start := 0
	for i := 0; i < cur; i++ {
		rows, blocks := m.itemRows(f, i)
		start += rows + len(blocks)
	}
	curRows, _ := m.itemRows(f, cur)
	h := m.diffHeight()
	if start < m.offset {
		m.offset = start
	}
	if start+curRows > m.offset+h {
		m.offset = start + curRows - h
	}
	// Do not leave empty space at the bottom, e.g. after folding lines.
	total := start
	for i := cur; i < len(f.lines); i++ {
		rows, blocks := m.itemRows(f, i)
		total += rows + len(blocks)
	}
	m.offset = max(0, min(m.offset, total-h))
}

func (m *Model) renderDiff() string {
	f := m.currFile()
	width, height := m.diffWidth(), m.diffHeight()
	theme := m.ctx.Theme
	if len(f.lines) == 0 {
		msg := "No diff to show: the file is binary, too large or only renamed."
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
			lipgloss.NewStyle().Foreground(theme.FaintText).Render(msg))
	}

	cur := m.currLine()
	selFrom, selTo := m.selection()
	avail := max(1, width-gutterWidth)
	num := func(n int) string {
		if n == 0 {
			return "    "
		}
		return fmt.Sprintf("%4d", n)
	}

	var out []string
	row := 0
	for i := 0; i < len(f.lines) && len(out) < height; i++ {
		l := f.lines[i]
		rows, blocks := m.itemRows(f, i)
		if row+rows+len(blocks) <= m.offset {
			row += rows + len(blocks)
			continue
		}

		var style lipgloss.Style
		marker := " "
		switch l.kind {
		case lineAdd:
			style = lipgloss.NewStyle().Foreground(m.gh("GREEN")).Background(m.tint("GREEN", 0.18))
			marker = "+"
		case lineDel:
			style = lipgloss.NewStyle().Foreground(m.gh("RED")).Background(m.tint("RED", 0.18))
			marker = "-"
		case lineHunk:
			style = lipgloss.NewStyle().Foreground(m.gh("BLUE")).Background(m.tint("BLUE", 0.12))
		case lineNote:
			style = lipgloss.NewStyle().Foreground(theme.FaintText).Italic(true)
		case lineExpanded:
			style = lipgloss.NewStyle().Foreground(theme.FaintText)
		case lineTail:
			style = lipgloss.NewStyle().Foreground(m.gh("BLUE")).Background(m.tint("BLUE", 0.12))
		default:
			style = lipgloss.NewStyle().Foreground(theme.PrimaryText)
		}
		gutterStyle := lipgloss.NewStyle().Foreground(theme.FaintText)
		switch {
		case i == cur:
			style = style.Background(theme.SelectedBackground).Bold(true)
			gutterStyle = gutterStyle.Background(theme.SelectedBackground).
				Foreground(theme.PrimaryText).Bold(true)
		case m.selFrom >= 0 && i >= selFrom && i <= selTo:
			style = style.Background(m.tint("YELLOW", 0.16))
			gutterStyle = gutterStyle.Background(m.tint("YELLOW", 0.16))
		}

		text := expandTabs(l.text)
		switch l.kind {
		case lineHunk:
			text = strings.TrimSpace(text)
			if i == cur {
				if start, end := f.gap(i); end >= start {
					text += fmt.Sprintf("   e: show 20 hidden lines · E: all %d", end-start+1)
				}
				if i+1 < len(f.lines) && f.lines[i+1].kind == lineExpanded {
					text += "   z: fold"
				}
			}
		case lineTail:
			text = "Rest of the file"
			if i == cur {
				text += "   e: show 20 more lines · E: all"
				if i > 0 && f.lines[i-1].kind == lineExpanded {
					text += " · z: fold"
				}
			}
		}
		wrapped := strings.Split(ansi.Hardwrap(text, avail, true), "\n")
		for r, part := range wrapped {
			gutter := strings.Repeat(" ", gutterWidth)
			if r == 0 && !l.isExpander() {
				gutter = " " + num(l.oldNo) + " " + num(l.newNo) + " " + marker
			} else if r == 0 {
				gutter = strings.Repeat(" ", gutterWidth-1) + "⋯"
			}
			var line string
			if colorName, ok := m.commentMark(f, i); ok && r == 0 {
				// A dot in the gutter flags the lines carrying comments.
				line = gutterStyle.Foreground(m.gh(colorName)).Bold(true).Render("●") +
					gutterStyle.Render(gutter[1:]) + style.Width(avail).Render(part)
			} else {
				line = gutterStyle.Render(gutter) + style.Width(avail).Render(part)
			}
			if row >= m.offset && len(out) < height {
				out = append(out, line)
			}
			row++
		}
		for _, b := range blocks {
			if row >= m.offset && len(out) < height {
				out = append(out, b)
			}
			row++
		}
	}
	for len(out) < height {
		out = append(out, "")
	}
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(strings.Join(out, "\n"))
}

func (m *Model) renderHint() string {
	faint := lipgloss.NewStyle().Foreground(m.ctx.Theme.FaintText)
	var hint string
	switch {
	case m.status != "":
		hint = lipgloss.NewStyle().Foreground(m.gh("YELLOW")).Render(m.status)
	case m.selFrom >= 0:
		hint = faint.Render("selecting lines: j/k extend • c comment the range • esc cancel")
	default:
		hint = faint.Render("j/k line • shift+↑/↓ or v select • c comment • x delete • e/z expand/fold • {/} hunk • [/] file • t files • S submit • q back")
		if f := m.currFile(); f != nil && len(f.lines) > 0 && f.lines[m.currLine()].kind == lineExpanded {
			hint = faint.Render("unchanged line, not commentable • z fold this block • Z fold all")
		}
	}
	return ansi.Truncate(" "+hint, m.width, "…")
}

func (m *Model) renderDialog() string {
	theme := m.ctx.Theme
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.PrimaryText).
		Padding(0, 1)
	faint := lipgloss.NewStyle().Foreground(theme.FaintText)
	bold := lipgloss.NewStyle().Bold(true).Foreground(theme.PrimaryText)

	switch m.mode {
	case modeCompose:
		f := m.currFile()
		from, to := m.selection()
		where := f.Filename
		_, toNo := f.lines[to].position()
		if from != to {
			_, fromNo := f.lines[from].position()
			where += fmt.Sprintf(" lines %d-%d", fromNo, toNo)
		} else {
			where += fmt.Sprintf(" line %d", toNo)
		}
		return box.Render(bold.Render("Comment on ") + faint.Render(ansi.Truncate(where, m.inputWidth()-12, "…")) +
			"\n\n" + m.input.View() + "\n\n" + faint.Render("ctrl+s save • esc cancel"))

	case modeSubmitChoice:
		var lines []string
		lines = append(lines, bold.Render("Finish your review"), "")
		if n := len(m.pending()); n > 0 {
			lines = append(lines, faint.Render(fmt.Sprintf("%d pending comment%s will be sent", n, plural(n))), "")
		}
		for i, e := range events {
			radio := "○"
			label := lipgloss.NewStyle().Foreground(theme.SecondaryText).Render(e.label)
			if i == m.eventIdx {
				radio = "●"
				label = bold.Render(e.label)
			}
			lines = append(lines, fmt.Sprintf("%s %d. %s", radio, i+1, label),
				"     "+faint.Render(e.hint))
		}
		lines = append(lines, "", faint.Render("j/k choose • enter continue • esc back"))
		return box.Render(strings.Join(lines, "\n"))

	case modeSubmitBody, modeSubmitting:
		e := events[m.eventIdx]
		status := faint.Render("ctrl+s submit • esc back")
		if m.mode == modeSubmitting {
			status = faint.Render("Submitting…")
		} else if m.status != "" {
			status = lipgloss.NewStyle().Foreground(m.gh("YELLOW")).Render(m.status)
		}
		return box.Render(bold.Render(e.label) + faint.Render(" · "+e.hint) +
			"\n\n" + m.input.View() + "\n\n" + status)
	}
	return ""
}
