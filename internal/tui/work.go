package tui

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/keys"
)

// handleWorkKey handles the keys of the "Current Work" view.
func (m *Model) handleWorkKey(msg tea.KeyMsg, row data.RowData) (tea.Cmd, bool) {
	wt, _ := row.(*data.Worktree)
	switch {
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
