// Package worksection is the "Current Work" view: the git worktrees of a
// local clone, listed like pull requests.
package worksection

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/config"
	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/section"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/table"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/constants"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/context"
	"github.com/dlvhdr/gh-dash/v4/internal/utils"
)

const SectionType = "worktree"

// The tabs are filters: every worktree, or only those with uncommitted
// changes.
var tabs = []config.SectionConfig{
	{Title: "All", Filters: ""},
	{Title: "Dirty", Filters: "is:dirty"},
}

type Model struct {
	section.BaseModel
	all       []data.Worktree
	Worktrees []data.Worktree // after the filter
}

type FetchedMsg struct {
	Worktrees []data.Worktree
	Err       error
	TaskId    string
}

func NewModel(id int, ctx *context.ProgramContext, cfg config.SectionConfig) *Model {
	m := &Model{}
	m.BaseModel = section.NewModel(ctx, section.NewSectionOptions{
		Id:          id,
		Config:      cfg,
		Type:        SectionType,
		Columns:     columns(),
		Singular:    "worktree",
		Plural:      "worktrees",
		LastUpdated: time.Now(),
		CreatedAt:   time.Now(),
	})
	return m
}

func InitSections(ctx *context.ProgramContext) []section.Section {
	sections := make([]section.Section, len(tabs))
	for i, t := range tabs {
		sections[i] = NewModel(i, ctx, t)
	}
	return sections
}

func FetchAllSections(ctx *context.ProgramContext, existing []section.Section) tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range section.ToImplSections[*Model](existing) {
		cmds = append(cmds, s.FetchNextPageSectionRows()...)
	}
	return tea.Batch(cmds...)
}

func columns() []table.Column {
	w := func(n int) *int { return utils.IntPtr(n) }
	return []table.Column{
		{Title: "", Width: w(2)},
		{Title: "Branch", Grow: utils.BoolPtr(true), NoWrap: true},
		{Title: "Issue", Width: w(7), NoWrap: true},
		{Title: "Status", Width: w(16), NoWrap: true},
		{Title: "P", Width: w(4), NoWrap: true},
		{Title: "Sync", Width: w(9), NoWrap: true},
		{Title: "Changes", Width: w(14), NoWrap: true},
		{Title: "Files", Width: w(7), NoWrap: true},
		{Title: "PR", Width: w(9), NoWrap: true},
		{Title: "󱦻", Width: w(5), NoWrap: true},
	}
}

func (m *Model) Update(msg tea.Msg) (section.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.IsSearchFocused() {
			switch msg.String() {
			case "ctrl+c", "esc":
				m.SearchBar.SetValue(m.SearchValue)
				return m, m.SetIsSearching(false)
			case "enter":
				m.SearchValue = m.SearchBar.Value()
				m.SetIsSearching(false)
				m.applyFilter()
				return m, nil
			}
		}

	case FetchedMsg:
		if msg.TaskId != m.LastFetch.TaskId {
			break
		}
		m.SetIsLoading(false)
		if msg.Worktrees != nil || msg.Err == nil {
			m.all = msg.Worktrees
		}
		m.applyFilter()
		m.Table.SetLastUpdated(time.Now())
	}

	search, searchCmd := m.SearchBar.Update(msg)
	m.SearchBar = search
	tbl, tableCmd := m.Table.Update(msg)
	m.Table = tbl
	return m, tea.Batch(searchCmd, tableCmd)
}

// applyFilter keeps the worktrees matching the filter: is:dirty, is:clean,
// has:pr, no:pr and words searched in the branch, issue and PR titles.
func (m *Model) applyFilter() {
	var res []data.Worktree
	tokens := strings.Fields(strings.ToLower(m.SearchValue))
	for _, w := range m.all {
		if matches(w, tokens) {
			res = append(res, w)
		}
	}
	m.Worktrees = res
	m.TotalCount = len(res)
	m.Table.SetRows(m.BuildRows())
	m.UpdateTotalItemsCount(m.TotalCount)
}

func matches(w data.Worktree, tokens []string) bool {
	text := strings.ToLower(w.Branch + " " + w.Path)
	if w.PR != nil {
		text += " " + strings.ToLower(w.PR.Title) + fmt.Sprintf(" #%d", w.PR.Number)
	}
	if w.Issue > 0 {
		t, _ := data.LookupProjectTitle(w.Repo, w.Issue)
		text += " " + strings.ToLower(t) + fmt.Sprintf(" #%d", w.Issue)
	}
	for _, t := range tokens {
		switch t {
		case "is:dirty":
			if !w.Dirty {
				return false
			}
		case "is:clean":
			if w.Dirty {
				return false
			}
		case "has:pr":
			if w.PR == nil {
				return false
			}
		case "no:pr":
			if w.PR != nil {
				return false
			}
		default:
			if !strings.Contains(text, t) {
				return false
			}
		}
	}
	return true
}

func (m *Model) BuildRows() []table.Row {
	rows := make([]table.Row, 0, len(m.Worktrees))
	for i := range m.Worktrees {
		rows = append(rows, m.row(&m.Worktrees[i]))
	}
	return rows
}

// RefreshRows re-renders the rows, e.g. once the scrum board is loaded.
func (m *Model) RefreshRows() {
	if m.Table.Rows != nil {
		m.Table.SetRows(m.BuildRows())
	}
}

func (m *Model) NumRows() int { return len(m.Worktrees) }

func (m *Model) GetCurrRow() data.RowData {
	idx := m.Table.GetCurrItem()
	if idx < 0 || idx >= len(m.Worktrees) {
		return nil
	}
	return &m.Worktrees[idx]
}

func (m *Model) FetchNextPageSectionRows() []tea.Cmd {
	if m == nil || !m.Ctx.Config.HasWork() {
		return nil
	}
	taskId := fmt.Sprintf("fetching_worktrees_%d_%d", m.Id, time.Now().UnixNano())
	m.LastFetch.TaskId = taskId
	m.LastFetch.At = time.Now()
	startCmd := m.Ctx.StartTask(context.Task{
		Id:           taskId,
		StartText:    "Reading the worktrees",
		FinishedText: "Worktrees read",
		State:        context.TaskStart,
	})
	cfg := m.Ctx.Config.Work
	dir, base := cfg.RepoDir(), cfg.Base
	id, typ := m.Id, m.Type
	fetchCmd := func() tea.Msg {
		wts, err := data.FetchWorktrees(dir, base)
		return constants.TaskFinishedMsg{
			SectionId:   id,
			SectionType: typ,
			TaskId:      taskId,
			Err:         err,
			Msg:         FetchedMsg{Worktrees: wts, Err: err, TaskId: taskId},
		}
	}
	return []tea.Cmd{startCmd, fetchCmd, m.SetIsLoading(true)}
}

func (m *Model) ResetRows() {
	m.Worktrees = nil
	m.BaseModel.ResetRows()
}

func (m *Model) GetItemSingularForm() string { return "worktree" }
func (m *Model) GetItemPluralForm() string   { return "worktrees" }
func (m *Model) GetTotalCount() int          { return m.TotalCount }

func (m *Model) GetPagerContent() string {
	if m.TotalCount == 0 {
		return ""
	}
	return fmt.Sprintf("%v %v • Worktree %v/%v", constants.WaitingIcon,
		m.LastUpdated().Format("01/02 15:04:05"), m.Table.GetCurrItem()+1, m.TotalCount)
}

// IsDataStale: worktrees change outside of gh-dash, read them again each
// time the view is shown if older than a few seconds.
func (m *Model) IsDataStale() bool {
	return time.Since(m.LastFetch.At) > 5*time.Second
}
