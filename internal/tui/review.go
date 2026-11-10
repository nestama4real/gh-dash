package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/common"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/prrow"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/reviewview"
)

func (m *Model) reviewSize() (int, int) {
	return m.ctx.ScreenWidth, max(1, m.ctx.ScreenHeight-common.FooterHeight)
}

// openReview opens the review mode on the selected PR.
func (m *Model) openReview(row data.RowData) tea.Cmd {
	pr, ok := row.(*prrow.Data)
	if !ok || pr == nil || pr.Primary == nil {
		return m.notifyErr("Select a pull request first")
	}
	w, h := m.reviewSize()
	review, cmd := reviewview.New(m.ctx, reviewview.PR{
		Repo:   pr.Primary.Repository.NameWithOwner,
		Number: pr.Primary.Number,
		Title:  pr.Primary.Title,
		Url:    pr.Primary.Url,
		Author: pr.Primary.Author.Login,
	}, w, h)
	m.review = &review
	return cmd
}

// updateReview gives the review mode the messages it owns: the keyboard
// while it is open and its own results. handled is false for the messages
// the rest of the dashboard must still process (tasks, spinner, resize...).
func (m *Model) updateReview(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case reviewview.ClosedMsg:
		m.review = nil
		if !msg.Submitted {
			return nil, true
		}
		return tea.Batch(m.notify("Review submitted"), m.prView.RefetchCurrRow()), true

	case tea.WindowSizeMsg:
		m.onWindowSizeChanged(msg)
		m.review.SetSize(m.reviewSize())
		return nil, true

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" && !m.review.IsTyping() {
			return tea.Quit, true
		}
		review, cmd := m.review.Update(msg)
		m.review = &review
		return cmd, true

	case tea.MouseMsg:
		return nil, true
	}

	review, cmd := m.review.Update(msg)
	m.review = &review
	return cmd, false
}
