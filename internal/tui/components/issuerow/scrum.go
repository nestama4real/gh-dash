package issuerow

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/common"
)

// Columns showing the issue type, the scrum board fields and linked PRs.

func (issue *Issue) faint() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(issue.Ctx.Theme.FaintText)
}

func (issue *Issue) none() string {
	return issue.faint().Render("–")
}

func (issue *Issue) ghStyle(colorName string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(common.GhColor(colorName, issue.Ctx.HasDarkBackground))
}

func (issue *Issue) renderType() string {
	t := issue.Data.IssueType
	if t == nil || t.Name == "" {
		return issue.none()
	}
	return issue.ghStyle(t.Color).Render(t.Name)
}

// projectValue renders a single select field of the scrum board with its
// option color, or a number field in the default text style.
func (issue *Issue) projectValue(field string) string {
	repo, number := issue.Data.GetRepoNameWithOwner(), issue.Data.Number
	v, ok := data.LookupProjectValue(repo, number, field)
	if !ok {
		return issue.none()
	}
	if v.Number != nil {
		return issue.getTextStyle().Render(data.FormatProjectNumber(*v.Number))
	}
	colorName := "GRAY"
	if f, ok := data.LookupProjectField(field); ok {
		if o, ok := f.OptionByName(v.Name); ok {
			colorName = o.Color
		}
	}
	return issue.ghStyle(colorName).Render(v.String())
}

func (issue *Issue) renderStatusField() string {
	return issue.projectValue(issue.Ctx.Config.ScrumBoard.GetStatusField())
}

func (issue *Issue) renderPriority() string {
	return issue.projectValue(issue.Ctx.Config.ScrumBoard.GetPriorityField())
}

func (issue *Issue) renderEta() string {
	return issue.projectValue(issue.Ctx.Config.ScrumBoard.GetEstimateField())
}

func (issue *Issue) renderLabels() string {
	labels := issue.Data.Labels.Nodes
	if len(labels) == 0 {
		return issue.none()
	}
	parts := make([]string, 0, len(labels))
	for _, l := range labels {
		st := issue.getTextStyle()
		if l.Color != "" {
			st = lipgloss.NewStyle().Foreground(lipgloss.Color("#" + strings.TrimPrefix(l.Color, "#")))
		}
		parts = append(parts, st.Render(l.Name))
	}
	return strings.Join(parts, issue.faint().Render(","))
}

func (issue *Issue) renderLinkedPRs() string {
	prs := issue.Data.ClosedByPullRequestsReferences.Nodes
	if len(prs) == 0 {
		return issue.none()
	}
	// Show the most relevant one: an open PR first, then merged, then closed.
	best := prs[0]
	rank := func(p data.LinkedPullRequest) int {
		switch {
		case p.State == "OPEN" && !p.IsDraft:
			return 0
		case p.State == "OPEN":
			return 1
		case p.State == "MERGED":
			return 2
		}
		return 3
	}
	for _, p := range prs[1:] {
		if rank(p) < rank(best) {
			best = p
		}
	}
	var st lipgloss.Style
	switch {
	case best.State == "MERGED":
		st = lipgloss.NewStyle().Foreground(issue.Ctx.Styles.Colors.MergedPR)
	case best.State == "CLOSED":
		st = issue.ghStyle("RED")
	case best.IsDraft:
		st = issue.faint()
	default:
		st = lipgloss.NewStyle().Foreground(issue.Ctx.Styles.Colors.OpenIssue)
	}
	out := st.Render(fmt.Sprintf("#%d", best.Number))
	if len(prs) > 1 {
		out += issue.faint().Render(fmt.Sprintf("+%d", len(prs)-1))
	}
	return out
}
