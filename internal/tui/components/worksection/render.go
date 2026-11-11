package worksection

import (
	"image/color"

	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/common"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/table"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/constants"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/context"
	"github.com/dlvhdr/gh-dash/v4/internal/utils"
)

type styles struct {
	text, faint, add, del, warn lipgloss.Style
}

func newStyles(ctx *context.ProgramContext) styles {
	return styles{
		text:  lipgloss.NewStyle().Foreground(ctx.Theme.PrimaryText),
		faint: lipgloss.NewStyle().Foreground(ctx.Theme.FaintText),
		add:   lipgloss.NewStyle().Foreground(ctx.Theme.SuccessText),
		del:   lipgloss.NewStyle().Foreground(ctx.Theme.ErrorText),
		warn:  lipgloss.NewStyle().Foreground(ctx.Theme.WarningText),
	}
}

func (m *Model) row(w *data.Worktree) table.Row {
	st := newStyles(m.Ctx)
	none := st.faint.Render("–")

	dirty := " "
	if w.Dirty {
		dirty = st.warn.Render("●")
	}

	issue, status, prio := none, none, none
	if w.Issue > 0 {
		issue = st.text.Render(fmt.Sprintf("#%d", w.Issue))
		sb := m.Ctx.Config.ScrumBoard
		status = projectValue(m.Ctx, w, sb.GetStatusField())
		prio = projectValue(m.Ctx, w, sb.GetPriorityField())
	}

	sync := none
	if w.Err == nil {
		sync = st.add.Render(fmt.Sprintf("↑%d", w.Ahead)) + " " + behind(st, w.Behind)
	}
	changes := none
	if w.Additions > 0 || w.Deletions > 0 {
		changes = st.add.Render(fmt.Sprintf("+%d", w.Additions)) + " " +
			st.del.Render(fmt.Sprintf("−%d", w.Deletions))
	}
	files := none
	if len(w.Files) > 0 {
		files = st.text.Render(fmt.Sprint(len(w.Files)))
	}

	branch := st.text.Render(w.GetTitle())
	if w.Err != nil {
		branch += " " + st.del.Render("("+w.Err.Error()+")")
	}

	return table.Row{
		dirty,
		branch,
		issue,
		status,
		prio,
		sync,
		changes,
		files,
		prCell(m.Ctx, w.PR),
		st.faint.Render(utils.TimeElapsed(w.ChangedAt)),
	}
}

func behind(st styles, n int) string {
	s := fmt.Sprintf("↓%d", n)
	if n > 0 {
		return st.warn.Render(s)
	}
	return st.faint.Render(s)
}

func prCell(ctx *context.ProgramContext, pr *data.BranchPR) string {
	if pr == nil {
		return lipgloss.NewStyle().Foreground(ctx.Theme.FaintText).Render("–")
	}
	icon, color := prIcon(ctx, pr)
	return lipgloss.NewStyle().Foreground(color).Render(fmt.Sprintf("%s #%d", icon, pr.Number))
}

func prIcon(ctx *context.ProgramContext, pr *data.BranchPR) (string, color.Color) {
	switch {
	case pr.State == "MERGED":
		return constants.MergedIcon, ctx.Styles.Colors.MergedPR
	case pr.State == "CLOSED":
		return constants.ClosedIcon, ctx.Styles.Colors.ClosedPR
	case pr.IsDraft:
		return constants.DraftIcon, ctx.Theme.FaintText
	}
	return constants.OpenIcon, ctx.Styles.Colors.OpenPR
}

// projectValue renders a single select field of the scrum board in its
// option color.
func projectValue(ctx *context.ProgramContext, w *data.Worktree, field string) string {
	faint := lipgloss.NewStyle().Foreground(ctx.Theme.FaintText)
	v, ok := data.LookupProjectValue(w.Repo, w.Issue, field)
	if !ok {
		return faint.Render("–")
	}
	if v.Number != nil {
		return lipgloss.NewStyle().Foreground(ctx.Theme.PrimaryText).Render(data.FormatProjectNumber(*v.Number))
	}
	colorName := "GRAY"
	if f, ok := data.LookupProjectField(field); ok {
		if o, ok := f.OptionByName(v.Name); ok {
			colorName = o.Color
		}
	}
	return lipgloss.NewStyle().Foreground(common.GhColor(colorName, ctx.HasDarkBackground)).Render(v.String())
}

// RenderPreview is the sidebar of a worktree: its links, changed files and
// commits.
func RenderPreview(ctx *context.ProgramContext, w *data.Worktree, width int) string {
	st := newStyles(ctx)
	bold := st.text.Bold(true)
	head := lipgloss.NewStyle().Foreground(ctx.Theme.SecondaryText).Bold(true)
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	trunc := func(s string, n int) string {
		if n <= 1 {
			return ""
		}
		r := []rune(s)
		if len(r) > n {
			return string(r[:n-1]) + "…"
		}
		return s
	}

	line(bold.Render(trunc(w.GetTitle(), width)))
	line(st.faint.Render(trunc(w.Path, width)))
	b.WriteString("\n")

	if w.Err != nil {
		line(st.del.Render(trunc(w.Err.Error(), width)))
		return b.String()
	}

	sync := fmt.Sprintf("%s %s %s", st.add.Render(fmt.Sprintf("↑%d", w.Ahead)), behind(st, w.Behind),
		st.faint.Render("vs "+w.Base))
	if w.Dirty {
		sync += "  " + st.warn.Render("● uncommitted changes")
	}
	line(sync)

	if w.PR != nil {
		icon, color := prIcon(ctx, w.PR)
		line(lipgloss.NewStyle().Foreground(color).Render(fmt.Sprintf("%s #%d", icon, w.PR.Number)) + " " +
			st.text.Render(trunc(w.PR.Title, width-8)))
	} else {
		line(st.faint.Render("No pull request"))
	}
	if w.Issue > 0 {
		title, _ := data.LookupProjectTitle(w.Repo, w.Issue)
		line(st.text.Render(fmt.Sprintf(" #%d", w.Issue)) + " " + st.text.Render(trunc(title, width-8)))
		sb := ctx.Config.ScrumBoard
		line("   " + projectValue(ctx, w, sb.GetStatusField()) + "  " +
			projectValue(ctx, w, sb.GetPriorityField()) + "  " +
			projectValue(ctx, w, sb.GetEstimateField()))
	}

	b.WriteString("\n")
	line(head.Render(fmt.Sprintf("Files changed (%d)", len(w.Files))) + "  " +
		st.add.Render(fmt.Sprintf("+%d", w.Additions)) + " " + st.del.Render(fmt.Sprintf("−%d", w.Deletions)))
	for _, f := range w.Files {
		stat := st.add.Render(fmt.Sprintf("+%d", f.Additions)) + " " + st.del.Render(fmt.Sprintf("−%d", f.Deletions))
		statW := lipgloss.Width(stat)
		if f.Binary {
			stat, statW = st.faint.Render("binary"), 6
		}
		mark := " "
		if f.Untracked {
			mark = st.add.Render("?")
		}
		name := trunc(f.Path, width-statW-3)
		pad := max(1, width-2-lipgloss.Width(name)-statW)
		line(mark + " " + st.text.Render(name) + strings.Repeat(" ", pad) + stat)
	}

	b.WriteString("\n")
	line(head.Render(fmt.Sprintf("Commits (%d)", len(w.Commits))))
	if len(w.Commits) == 0 {
		line(st.faint.Render("No commit since " + w.Base))
	}
	for _, c := range w.Commits {
		age := utils.TimeElapsed(c.At)
		line(st.warn.Render(c.Sha) + " " + st.text.Render(trunc(c.Subject, width-len(c.Sha)-len(age)-3)) +
			" " + st.faint.Render(age))
	}
	return b.String()
}
