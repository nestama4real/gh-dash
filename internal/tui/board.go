package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/config"
	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/boardsection"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/section"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/keys"
)

// enabledViews lists the views reachable with the view switcher, in order.
func (m *Model) enabledViews() []config.ViewType {
	views := []config.ViewType{config.PRsView, config.IssuesView}
	if m.ctx.Config.HasScrumBoard() {
		views = append(views, config.BoardView)
	}
	if config.IsFeatureEnabled(config.FF_REPO_VIEW) {
		views = append(views, config.RepoView)
	}
	if !m.ctx.Config.HideNotifications {
		views = append(views, config.NotificationsView)
	}
	return views
}

func (m *Model) nextView() config.ViewType {
	views := m.enabledViews()
	for i, v := range views {
		if v == m.ctx.View {
			return views[(i+1)%len(views)]
		}
	}
	return views[0]
}

// onBoardFetched stores the project and rebuilds the tabs when the
// project's views changed (e.g. on the first fetch).
func (m *Model) onBoardFetched(msg boardsection.BoardFetchedMsg) tea.Cmd {
	nextCmd := m.boardStore.HandleFetched(m.ctx, m.currSectionId, msg)
	if boardsection.ViewsChanged(m.board, m.boardStore) {
		old := m.board
		m.board = boardsection.InitSections(m.ctx, m.boardStore)
		for _, s := range m.board {
			for _, o := range old {
				s.(*boardsection.Model).CopyStateFrom(o.(*boardsection.Model))
			}
		}
		if m.currSectionId >= len(m.board) && m.ctx.View == config.BoardView {
			m.setCurrSectionId(0)
		}
	}
	if m.ctx.View != config.BoardView {
		return nextCmd
	}
	m.updateTabs()
	m.tabs.SetCurrSectionId(m.currSectionId)
	return tea.Batch(nextCmd, m.onViewedRowChanged())
}

// handleBoardKey handles the keys of the board view. It returns false when
// the key is not a board key, so that the generic handling goes on.
func (m *Model) handleBoardKey(
	msg tea.KeyMsg,
	currSection section.Section,
	currRowData data.RowData,
) (tea.Cmd, bool) {
	board, ok := currSection.(*boardsection.Model)
	if !ok {
		return nil, false
	}

	issueSelected := func() bool {
		issue, ok := currRowData.(*data.IssueData)
		return ok && issue != nil && issue.Number > 0
	}
	noIssue := func() (tea.Cmd, bool) {
		return m.notifyErr("Select an issue first"), true
	}

	switch {
	case key.Matches(msg, keys.BoardKeys.PrevColumn):
		board.PrevColumn()
		return m.onViewedRowChanged(), true

	case key.Matches(msg, keys.BoardKeys.NextColumn):
		board.NextColumn()
		return m.onViewedRowChanged(), true

	case key.Matches(msg, keys.BoardKeys.MoveLeft):
		cmd := board.MoveCard(-1)
		return tea.Batch(cmd, m.onViewedRowChanged()), true

	case key.Matches(msg, keys.BoardKeys.MoveRight):
		cmd := board.MoveCard(1)
		return tea.Batch(cmd, m.onViewedRowChanged()), true

	case key.Matches(msg, keys.BoardKeys.EditFields):
		return board.OpenEditor(), true

	case key.Matches(msg, keys.BoardKeys.ToggleOldDone):
		board.ToggleOldDone()
		return m.onViewedRowChanged(), true

	case key.Matches(msg, keys.BoardKeys.Preview):
		m.sidebar.IsOpen = !m.sidebar.IsOpen
		m.syncMainContentDimensions()
		m.syncProgramContext()
		return m.syncSidebar(), true

	case key.Matches(msg, keys.BoardKeys.ClosePreview):
		if m.sidebar.IsOpen {
			m.sidebar.IsOpen = false
			m.syncMainContentDimensions()
			m.syncProgramContext()
		}
		return nil, true

	case key.Matches(msg, m.keys.OpenGithub):
		if currRowData == nil {
			return noIssue()
		}
		return m.openBrowser(), true

	case key.Matches(msg, keys.IssueKeys.Label):
		if !issueSelected() {
			return noIssue()
		}
		return m.openSidebarForInput(m.issueSidebar.SetIsLabeling), true

	case key.Matches(msg, keys.IssueKeys.Assign):
		if !issueSelected() {
			return noIssue()
		}
		return m.openSidebarForInput(m.issueSidebar.SetIsAssigning), true

	case key.Matches(msg, keys.IssueKeys.Unassign):
		if !issueSelected() {
			return noIssue()
		}
		return m.openSidebarForInput(m.issueSidebar.SetIsUnassigning), true

	case key.Matches(msg, keys.IssueKeys.Comment):
		if !issueSelected() {
			return noIssue()
		}
		return m.openSidebarForInput(m.issueSidebar.SetIsCommenting), true

	case key.Matches(msg, keys.IssueKeys.Close):
		if !issueSelected() {
			return noIssue()
		}
		return m.promptConfirmation(currSection, "close"), true

	case key.Matches(msg, keys.IssueKeys.Reopen):
		if !issueSelected() {
			return noIssue()
		}
		return m.promptConfirmation(currSection, "reopen"), true

	case key.Matches(msg, keys.IssueKeys.ViewPRs):
		return m.switchSelectedView(), true
	}

	return nil, false
}
