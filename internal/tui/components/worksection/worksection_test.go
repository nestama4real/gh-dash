package worksection

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dlvhdr/gh-dash/v4/internal/config"
	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/context"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/theme"
)

func testCtx(t *testing.T) *context.ProgramContext {
	cfg, err := config.ParseConfig(config.Location{ConfigFlag: "../../../config/testdata/test-config.yml"})
	require.NoError(t, err)
	cfg.Work = &config.WorkConfig{Repo: "/nonexistent"}
	ctx := &context.ProgramContext{Config: &cfg, View: config.WorkView,
		MainContentWidth: 120, MainContentHeight: 20, ScreenWidth: 120, ScreenHeight: 30}
	ctx.Theme = theme.ParseTheme(ctx.Config)
	ctx.Styles = context.InitStyles(ctx.Theme)
	ctx.StartTask = func(task context.Task) tea.Cmd { return nil }
	return ctx
}

func TestFilterRows(t *testing.T) {
	ctx := testCtx(t)
	m := NewModel(1, ctx, tabs[1])
	m.UpdateProgramContext(ctx)
	m.FetchNextPageSectionRows()
	wts := []data.Worktree{
		{Branch: "feature/226-edit-teams", Issue: 226},
		{Branch: "fix/409-ended", Issue: 409, Dirty: true},
	}
	s, _ := m.Update(FetchedMsg{Worktrees: wts, TaskId: m.LastFetch.TaskId})
	m = s.(*Model)
	require.Equal(t, 1, m.NumRows())
	view := ansi.Strip(m.View())
	require.Equal(t, 1, strings.Count(view, "fix/409-ended"), view)
	require.NotContains(t, view, "feature/226")
}

func TestFilterRowsAfterResize(t *testing.T) {
	ctx := testCtx(t)
	m := NewModel(1, ctx, tabs[1])
	m.UpdateProgramContext(ctx)
	m.FetchNextPageSectionRows()
	wts := []data.Worktree{
		{Branch: "feature/226-edit-teams", Issue: 226},
		{Branch: "fix/409-ended", Issue: 409, Dirty: true},
	}
	s, _ := m.Update(FetchedMsg{Worktrees: wts, TaskId: m.LastFetch.TaskId})
	m = s.(*Model)
	ctx.MainContentWidth = 160
	m.UpdateProgramContext(ctx)
	m.RefreshRows()
	view := ansi.Strip(m.View())
	require.Equal(t, 1, strings.Count(view, "fix/409-ended"), view)
}
