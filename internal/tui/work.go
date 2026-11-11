package tui

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/reviewview"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/keys"
)

// handleWorkKey handles the keys of the "Current Work" view.
func (m *Model) handleWorkKey(msg tea.KeyMsg, row data.RowData) (tea.Cmd, bool) {
	wt, _ := row.(*data.Worktree)
	switch {
	case key.Matches(msg, keys.WorkKeys.Review):
		return m.openWorkReview(wt), true
	case key.Matches(msg, keys.WorkKeys.Commits):
		cmd := m.openWorkReview(wt)
		if m.review != nil {
			m.review.OpenCommitPicker()
		}
		return cmd, true
	case key.Matches(msg, m.keys.OpenGithub):
		return m.openBrowser(), true
	case key.Matches(msg, keys.WorkKeys.OpenIssue):
		if wt == nil || wt.Issue == 0 {
			return m.notifyErr("No issue linked to this worktree"), true
		}
		return m.openUrl(fmt.Sprintf("https://github.com/%s/issues/%d", wt.Repo, wt.Issue)), true
	}
	return nil, false
}

// openWorkReview opens the review mode on a worktree's changes.
func (m *Model) openWorkReview(wt *data.Worktree) tea.Cmd {
	if wt == nil {
		return m.notifyErr("Select a worktree first")
	}
	if wt.Err != nil {
		return m.notifyErr(wt.Err.Error())
	}
	pr := reviewview.PR{
		Repo:  wt.Repo,
		Title: wt.GetTitle(),
		Local: &reviewview.Local{
			Dir:       wt.Path,
			Branch:    wt.GetTitle(),
			Base:      wt.Base,
			MergeBase: wt.MergeBase,
			Commits:   wt.Commits,
		},
	}
	if wt.PR != nil {
		pr.Number, pr.Title, pr.Url = wt.PR.Number, wt.PR.Title, wt.PR.Url
	}
	w, h := m.reviewSize()
	review, cmd := reviewview.New(m.ctx, pr, w, h)
	m.review = &review
	return cmd
}
