package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/config"
	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/boardsection"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/issuessection"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/section"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/worksection"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/keys"
)

// enabledViews lists the views reachable with the view switcher, in order.
func (m *Model) enabledViews() []config.ViewType {
	var views []config.ViewType
	if m.ctx.Config.HasScrumBoard() {
		views = append(views, config.BoardView)
	}
	views = append(views, config.PRsView, config.IssuesView)
	if m.ctx.Config.HasWork() {
		views = append(views, config.WorkView)
	}
	if config.IsFeatureEnabled(config.FF_REPO_VIEW) {
		views = append(views, config.RepoView)
	}
	if !m.ctx.Config.HideNotifications {
		views = append(views, config.NotificationsView)
	}
	return views
}

func (m *Model) nextView(dir int) config.ViewType {
	if m.ctx.View == config.NotificationsView && dir > 0 {
		// As upstream: notifications lead to the PRs.
		return config.PRsView
	}
	views := m.enabledViews()
	for i, v := range views {
		if v == m.ctx.View {
			return views[(i+dir+len(views))%len(views)]
		}
	}
	return views[0]
}

// onBoardFetched stores the project and rebuilds the tabs when the
// project's views changed (e.g. on the first fetch).
func (m *Model) onBoardFetched(msg boardsection.BoardFetchedMsg) tea.Cmd {
	nextCmd := m.boardStore.HandleFetched(m.ctx, m.currSectionId, msg)
	if msg.Err == nil && msg.Next == nil {
		nextCmd = tea.Batch(nextCmd, m.onBoardIndexed())
	}
	if boardsection.ViewsChanged(m.board, m.boardStore, m.ctx.Config.ScrumBoard) {
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

// onBoardIndexed refreshes what depends on the scrum board in other views:
// the scrum columns of issue rows and the PR sections filtered on the
// current sprint.
func (m *Model) onBoardIndexed() tea.Cmd {
	m.refreshIssueRows()
	var cmds []tea.Cmd
	for _, s := range m.prs {
		if data.HasBoardPRQualifier(s.GetFilters()) {
			s.ResetRows()
			cmds = append(cmds, s.FetchNextPageSectionRows()...)
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) refreshIssueRows() {
	for _, s := range m.issues {
		if is, ok := s.(*issuessection.Model); ok {
			is.RefreshRows()
		}
	}
	for _, s := range m.work {
		if ws, ok := s.(*worksection.Model); ok {
			ws.RefreshRows()
		}
	}
}

// fetchBoardInBackground keeps the board fresh for the other views.
func (m *Model) fetchBoardInBackground() tea.Cmd {
	if !m.ctx.Config.HasScrumBoard() || m.ctx.View == config.BoardView {
		return nil
	}
	return boardsection.Fetch(m.ctx, m.boardStore, 0)
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
		return m.onViewedRowChangedSoon(), true

	case key.Matches(msg, keys.BoardKeys.NextColumn):
		board.NextColumn()
		return m.onViewedRowChangedSoon(), true

	case key.Matches(msg, keys.BoardKeys.MoveLeft):
		cmd := board.MoveCard(-1)
		return tea.Batch(cmd, m.onViewedRowChanged()), true

	case key.Matches(msg, keys.BoardKeys.MoveRight):
		cmd := board.MoveCard(1)
		return tea.Batch(cmd, m.onViewedRowChanged()), true

	case key.Matches(msg, keys.BoardKeys.EditFields):
		return board.OpenEditor(), true

	case key.Matches(msg, keys.BoardKeys.EditEstimate):
		if cmd := board.OpenFieldEditor(boardsection.IsEstimateField); cmd != nil ||
			board.IsCapturingInput() {
			return cmd, true
		}
		return m.notifyErr("This project has no estimated time field"), true

	case key.Matches(msg, keys.BoardKeys.EditActual):
		if cmd := board.OpenFieldEditor(boardsection.IsActualTimeField); cmd != nil ||
			board.IsCapturingInput() {
			return cmd, true
		}
		return m.notifyErr("This project has no actual time field"), true

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
