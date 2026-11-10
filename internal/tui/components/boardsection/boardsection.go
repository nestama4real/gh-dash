// Package boardsection renders a GitHub Project (v2) as a kanban board.
//
// Every project view becomes a section (a tab). All sections share a single
// Store holding the project, so the whole board is fetched once and edits
// made from one tab are visible in the others.
package boardsection

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/config"
	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/search"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/section"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/table"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/tasks"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/constants"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/context"
)

const SectionType = "board"

const (
	noStatusKey = "__no_status__"
	oldDoneKey  = "__old_done__"
)

var doneInRe = regexp.MustCompile(`(?i)^done in (\d+)$`)

// Store is shared by all the board sections.
type Store struct {
	Project     *data.ProjectData
	FetchedAt   time.Time
	IsLoading   bool
	Err         error
	ShowOldDone bool
	fetching    bool
	lastTaskId  string
	staging     *data.ProjectData
	cacheLoaded bool
}

func NewStore() *Store {
	return &Store{}
}

// LoadCache shows the last fetched board right away, while it is refreshed.
func (s *Store) LoadCache(cfg *config.Config) {
	if s.cacheLoaded || !cfg.HasScrumBoard() {
		return
	}
	s.cacheLoaded = true
	if p, at, ok := data.LoadCachedProject(cfg.ScrumBoard.Owner, cfg.ScrumBoard.Number); ok {
		s.Project, s.FetchedAt = p, at
		s.Reindex(cfg)
	}
}

// Reindex publishes the project to the other views (issue rows, PR filters).
func (s *Store) Reindex(cfg *config.Config) {
	data.SetProjectIndex(s.Project, cfg.ScrumBoard.GetStatusField())
}

type column struct {
	key         string
	title       string
	color       string
	description string
	collapsed   bool
	optionId    string
	items       []*data.ProjectItem
}

type Model struct {
	section.BaseModel
	store *Store
	view  data.ProjectView
	terms []filterTerm

	columns   []column
	selCol    int
	selRow    map[string]int
	rowOffset map[string]int
	colOffset int
	follow    string // item id to select on next rebuild

	editor *fieldEditor
}

func NewModel(
	id int,
	ctx *context.ProgramContext,
	store *Store,
	view data.ProjectView,
) *Model {
	m := &Model{
		store:     store,
		view:      view,
		selRow:    map[string]int{},
		rowOffset: map[string]int{},
	}
	m.BaseModel = section.NewModel(ctx, section.NewSectionOptions{
		Id:      id,
		Config:  config.SectionConfig{Title: view.Name, Filters: view.Filter},
		Type:    SectionType,
		Columns: []table.Column{},
	})
	m.SingularForm = "Item"
	m.PluralForm = "Items"
	m.IsFilteredByCurrentRemote = false
	m.SearchValue = view.Filter
	m.SearchBar = search.NewModel(ctx, search.SearchOptions{
		Prefix:       constants.SearchIcon,
		InitialValue: view.Filter,
		Placeholder:  "Filter by keyword or by field",
	})
	m.terms = parseFilter(view.Filter)
	return m
}

func boardViews(store *Store) []data.ProjectView {
	var views []data.ProjectView
	if store.Project == nil {
		return nil
	}
	for _, v := range store.Project.Views {
		if v.Layout == "BOARD_LAYOUT" {
			views = append(views, v)
		}
	}
	if len(views) == 0 {
		views = append(views, store.Project.Views...)
	}
	return views
}

// InitSections builds one section per board view of the project. Before the
// project is fetched, a single placeholder section is returned.
func InitSections(ctx *context.ProgramContext, store *Store) []section.Section {
	views := boardViews(store)
	if len(views) == 0 {
		views = []data.ProjectView{{Name: ctx.Config.ScrumBoard.GetTitle()}}
	}
	sections := make([]section.Section, 0, len(views))
	for i, v := range views {
		sections = append(sections, NewModel(i, ctx, store, v))
	}
	return sections
}

// ViewsChanged reports whether the sections no longer match the project views.
func ViewsChanged(sections []section.Section, store *Store) bool {
	views := boardViews(store)
	if len(views) == 0 {
		return false
	}
	if len(views) != len(sections) {
		return true
	}
	for i, v := range views {
		s, ok := sections[i].(*Model)
		if !ok || s.view.Id != v.Id || s.view.Name != v.Name || s.view.Filter != v.Filter {
			return true
		}
	}
	return false
}

// CopyStateFrom keeps the user's position and edited filter across a rebuild.
func (m *Model) CopyStateFrom(old *Model) {
	if old.view.Id != m.view.Id {
		return
	}
	m.selCol, m.colOffset = old.selCol, old.colOffset
	m.selRow, m.rowOffset = old.selRow, old.rowOffset
	if old.SearchValue != old.view.Filter {
		m.SearchValue = old.SearchValue
		m.SearchBar.SetValue(old.SearchValue)
		m.terms = parseFilter(old.SearchValue)
	}
}

// BoardFetchedMsg carries one page of the project. The last page (or an
// error) is wrapped in a TaskFinishedMsg so that the footer task completes.
type BoardFetchedMsg struct {
	Project *data.ProjectData
	First   bool
	Next    *string
	TaskId  string
	Err     error
}

type fieldUpdatedMsg struct {
	itemId    string
	fieldName string
	previous  *data.ProjectFieldValue
	err       error
}

// Fetch starts fetching the whole project unless a fetch is already running.
func Fetch(ctx *context.ProgramContext, store *Store, sectionId int) tea.Cmd {
	if store.fetching || !ctx.Config.HasScrumBoard() {
		return nil
	}
	store.fetching = true
	store.IsLoading = true
	cfg := ctx.Config.ScrumBoard
	taskId := fmt.Sprintf("fetching_board_%d", time.Now().UnixNano())
	store.lastTaskId = taskId
	task := context.Task{
		Id:           taskId,
		StartText:    fmt.Sprintf("Fetching %s", cfg.GetTitle()),
		FinishedText: fmt.Sprintf("%s has been fetched", cfg.GetTitle()),
		State:        context.TaskStart,
	}
	return tea.Batch(ctx.StartTask(task), fetchPage(ctx, taskId, sectionId, nil))
}

func fetchPage(ctx *context.ProgramContext, taskId string, sectionId int, after *string) tea.Cmd {
	cfg := ctx.Config.ScrumBoard
	return func() tea.Msg {
		page, next, err := data.FetchProjectPage(cfg.Owner, cfg.Number, after)
		msg := BoardFetchedMsg{
			Project: page, First: after == nil, Next: next, TaskId: taskId, Err: err,
		}
		if err == nil && next != nil {
			return msg
		}
		return constants.TaskFinishedMsg{
			SectionId:   sectionId,
			SectionType: SectionType,
			TaskId:      taskId,
			Err:         err,
			Msg:         msg,
		}
	}
}

// HandleFetched stores a fetched page and returns the command fetching the
// next one. Pages are shown as they arrive only when nothing was displayed
// yet; otherwise the current board stays until the new one is complete.
func (s *Store) HandleFetched(ctx *context.ProgramContext, sectionId int, msg BoardFetchedMsg) tea.Cmd {
	if msg.TaskId != s.lastTaskId {
		return nil
	}
	if msg.Err != nil {
		s.fetching, s.IsLoading, s.staging = false, false, nil
		s.Err = msg.Err
		return nil
	}
	if msg.First || s.staging == nil {
		s.staging = msg.Project
	} else {
		s.staging.Items = append(s.staging.Items, msg.Project.Items...)
	}
	if s.Project == nil {
		s.Project = s.staging
	}
	if msg.Next != nil {
		return fetchPage(ctx, msg.TaskId, sectionId, msg.Next)
	}

	s.Project, s.staging = s.staging, nil
	s.FetchedAt = time.Now()
	s.fetching, s.IsLoading, s.Err = false, false, nil
	s.Reindex(ctx.Config)
	cfg := ctx.Config.ScrumBoard
	project, at := s.Project, s.FetchedAt
	return func() tea.Msg {
		data.SaveCachedProject(cfg.Owner, cfg.Number, project, at)
		return nil
	}
}

// ---------------------------------------------------------------------------
// Columns

func (m *Model) statusField() (data.ProjectField, bool) {
	if m.store.Project == nil {
		return data.ProjectField{}, false
	}
	return m.store.Project.Field(m.Ctx.Config.ScrumBoard.GetStatusField())
}

// rebuild recomputes the columns from the store and the current filter.
func (m *Model) rebuild() {
	m.columns = nil
	project := m.store.Project
	if project == nil {
		return
	}
	statusField, hasStatus := m.statusField()
	fc := newFilterContext(project, m.Ctx.User)

	collapse := m.Ctx.Config.ScrumBoard.ShouldCollapseOldDone() && !m.store.ShowOldDone
	maxDone := -1
	if hasStatus {
		for _, o := range statusField.Options {
			if mm := doneInRe.FindStringSubmatch(o.Name); mm != nil {
				n, _ := strconv.Atoi(mm[1])
				maxDone = max(maxDone, n)
			}
		}
	}
	minOld, maxOld := -1, -1
	isOldDone := func(name string) bool {
		mm := doneInRe.FindStringSubmatch(name)
		if mm == nil {
			return false
		}
		n, _ := strconv.Atoi(mm[1])
		if n >= maxDone {
			return false
		}
		if minOld == -1 || n < minOld {
			minOld = n
		}
		maxOld = max(maxOld, n)
		return true
	}

	byKey := map[string]*column{}
	var order []string
	add := func(c column) {
		byKey[c.key] = &c
		order = append(order, c.key)
	}
	add(column{
		key: noStatusKey, title: "No " + statusField.Name, color: "GRAY",
		description: "Items without a " + strings.ToLower(statusField.Name),
	})
	if !hasStatus {
		byKey[noStatusKey].title = "Items"
		byKey[noStatusKey].description = ""
	}
	oldDoneAdded := false
	for _, o := range statusField.Options {
		if collapse && isOldDone(o.Name) {
			if !oldDoneAdded {
				add(column{
					key: oldDoneKey, title: "Done", color: "RED",
					description: "z to expand", collapsed: true,
				})
				oldDoneAdded = true
			}
			byKey[o.Id] = byKey[oldDoneKey]
			continue
		}
		add(column{
			key: o.Id, optionId: o.Id, title: o.Name, color: o.Color,
			description: o.Description,
		})
	}

	for i := range project.Items {
		item := &project.Items[i]
		if !fc.matchesAll(item, m.terms) {
			continue
		}
		key := noStatusKey
		if v, ok := item.FieldValues[statusField.Name]; ok && hasStatus {
			if _, known := byKey[v.OptionId]; known {
				key = v.OptionId
			}
		}
		c := byKey[key]
		c.items = append(c.items, item)
	}

	if oldDoneAdded {
		if minOld == maxOld {
			byKey[oldDoneKey].title = fmt.Sprintf("Done in %d", minOld)
		} else {
			byKey[oldDoneKey].title = fmt.Sprintf("Done %d–%d", minOld, maxOld)
		}
	}

	for _, k := range order {
		c := byKey[k]
		if k == noStatusKey && len(c.items) == 0 && hasStatus {
			continue
		}
		m.columns = append(m.columns, *c)
	}

	if m.follow != "" {
		for ci, c := range m.columns {
			for ri, it := range c.items {
				if it.Id == m.follow {
					m.selCol = ci
					m.selRow[c.key] = ri
				}
			}
		}
		m.follow = ""
	}
	m.selCol = clamp(m.selCol, 0, len(m.columns)-1)
	if c := m.currColumn(); c != nil {
		m.selRow[c.key] = clamp(m.selRow[c.key], 0, len(c.items)-1)
	}
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}

func (m *Model) currColumn() *column {
	if m.selCol < 0 || m.selCol >= len(m.columns) {
		return nil
	}
	return &m.columns[m.selCol]
}

func (m *Model) currItem() *data.ProjectItem {
	c := m.currColumn()
	if c == nil || c.collapsed || len(c.items) == 0 {
		return nil
	}
	return c.items[clamp(m.selRow[c.key], 0, len(c.items)-1)]
}

func (m *Model) setRow(row int) int {
	m.rebuild()
	c := m.currColumn()
	if c == nil {
		return 0
	}
	m.selRow[c.key] = clamp(row, 0, len(c.items)-1)
	return m.selRow[c.key]
}

// ---------------------------------------------------------------------------
// Navigation (section.Table)

func (m *Model) NumRows() int {
	m.rebuild()
	if c := m.currColumn(); c != nil && !c.collapsed {
		return len(c.items)
	}
	return 0
}

func (m *Model) CurrRow() int {
	m.rebuild()
	if c := m.currColumn(); c != nil {
		return m.selRow[c.key]
	}
	return 0
}

func (m *Model) NextRow() int   { return m.setRow(m.CurrRow() + 1) }
func (m *Model) PrevRow() int   { return m.setRow(m.CurrRow() - 1) }
func (m *Model) FirstItem() int { return m.setRow(0) }
func (m *Model) LastItem() int  { return m.setRow(1 << 30) }

func (m *Model) PrevColumn() {
	m.rebuild()
	m.selCol = clamp(m.selCol-1, 0, len(m.columns)-1)
}

func (m *Model) NextColumn() {
	m.rebuild()
	m.selCol = clamp(m.selCol+1, 0, len(m.columns)-1)
}

func (m *Model) ToggleOldDone() {
	m.rebuild()
	key, idx := "", m.selCol
	if c := m.currColumn(); c != nil {
		key = c.key
	}
	m.store.ShowOldDone = !m.store.ShowOldDone
	m.rebuild()
	if key == oldDoneKey {
		// Expanded: the first older "Done in" column takes the folded one's place.
		m.selCol = clamp(idx, 0, len(m.columns)-1)
		return
	}
	for i, c := range m.columns {
		if c.key == key {
			m.selCol = i
			return
		}
	}
	// The selected column has just been folded.
	for i, c := range m.columns {
		if c.key == oldDoneKey {
			m.selCol = i
			return
		}
	}
}

func (m *Model) GetCurrRow() data.RowData {
	m.rebuild()
	item := m.currItem()
	if item == nil {
		return nil
	}
	return &item.Content
}

// CurrItem exposes the selected project item.
func (m *Model) CurrItem() *data.ProjectItem {
	m.rebuild()
	return m.currItem()
}

func (m *Model) FetchNextPageSectionRows() []tea.Cmd {
	if m.store.Project != nil && !m.store.IsLoading {
		return nil
	}
	if cmd := Fetch(m.Ctx, m.store, m.Id); cmd != nil {
		return []tea.Cmd{cmd}
	}
	return nil
}

func (m *Model) BuildRows() []table.Row { return nil }

func (m *Model) ResetRows() {
	// Keep showing the current board while the refresh is in flight.
	m.store.IsLoading = true
}

// GetIsLoading only reports the first load: a refresh keeps the current
// board (and the tab counts) on screen.
func (m *Model) GetIsLoading() bool {
	return m.store.Project == nil && m.store.Err == nil
}

func (m *Model) SetIsLoading(val bool) tea.Cmd {
	m.store.IsLoading = val
	return nil
}

func (m *Model) IsDataStale() bool {
	return m.store.Project == nil || time.Since(m.store.FetchedAt) > 5*time.Minute
}

func (m *Model) GetTotalCount() int {
	m.rebuild()
	n := 0
	for _, c := range m.columns {
		n += len(c.items)
	}
	return n
}

func (m *Model) GetItemSingularForm() string { return "Item" }
func (m *Model) GetItemPluralForm() string   { return "Items" }

func (m *Model) GetPagerContent() string {
	m.rebuild()
	if m.store.Project == nil {
		return ""
	}
	pos := ""
	if c := m.currColumn(); c != nil {
		if c.collapsed {
			pos = fmt.Sprintf("%s (%d, collapsed)", c.title, len(c.items))
		} else if len(c.items) > 0 {
			pos = fmt.Sprintf("%s %d/%d", c.title, m.selRow[c.key]+1, len(c.items))
		} else {
			pos = fmt.Sprintf("%s (empty)", c.title)
		}
	}
	content := fmt.Sprintf("%v %v • %s • %d items",
		constants.WaitingIcon,
		m.store.FetchedAt.Format("01/02 15:04:05"),
		pos,
		m.GetTotalCount(),
	)
	return m.Ctx.Styles.ListViewPort.PagerStyle.Render(content)
}

// IsCapturingInput is true while the field editor owns the keyboard.
func (m *Model) IsCapturingInput() bool {
	return m.editor != nil
}

// ---------------------------------------------------------------------------
// Update

func (m *Model) Update(msg tea.Msg) (section.Section, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.editor != nil {
			return m, m.updateEditor(msg)
		}

		if m.IsSearchFocused() {
			switch msg.String() {
			case "ctrl+c", "esc":
				m.SearchBar.SetValue(m.SearchValue)
				return m, m.SetIsSearching(false)
			case "enter":
				m.SearchValue = m.SearchBar.Value()
				m.terms = parseFilter(m.SearchValue)
				m.SetIsSearching(false)
				m.rebuild()
				// Land on the first column that still has cards.
				if c := m.currColumn(); c == nil || len(c.items) == 0 {
					for i, col := range m.columns {
						if len(col.items) > 0 && !col.collapsed {
							m.selCol = i
							break
						}
					}
				}
				return m, nil
			}
			break
		}

		if m.IsPromptConfirmationFocused() {
			switch msg.String() {
			case "ctrl+c", "esc":
				m.PromptConfirmationBox.Reset()
				return m, m.SetIsPromptConfirmationShown(false)
			case "enter":
				input := m.PromptConfirmationBox.Value()
				row := m.GetCurrRow()
				if (input == "Y" || input == "y") && row != nil {
					sid := tasks.SectionIdentifier{Id: m.Id, Type: SectionType}
					switch m.GetPromptConfirmationAction() {
					case "close":
						cmd = tasks.CloseIssue(m.Ctx, sid, row)
					case "reopen":
						cmd = tasks.ReopenIssue(m.Ctx, sid, row)
					}
				}
				m.PromptConfirmationBox.Reset()
				return m, tea.Batch(cmd, m.SetIsPromptConfirmationShown(false))
			}
		}

	case tasks.UpdateIssueMsg:
		m.applyIssueUpdate(msg)

	case fieldUpdatedMsg:
		if msg.err != nil {
			m.revertField(msg)
		}
	}

	searchBar, searchCmd := m.SearchBar.Update(msg)
	m.SearchBar = searchBar

	prompt, promptCmd := m.PromptConfirmationBox.Update(msg)
	m.PromptConfirmationBox = prompt

	return m, tea.Batch(cmd, searchCmd, promptCmd)
}

func (m *Model) findItem(id string) *data.ProjectItem {
	if m.store.Project == nil {
		return nil
	}
	for i := range m.store.Project.Items {
		if m.store.Project.Items[i].Id == id {
			return &m.store.Project.Items[i]
		}
	}
	return nil
}

func (m *Model) applyIssueUpdate(msg tasks.UpdateIssueMsg) {
	if m.store.Project == nil {
		return
	}
	for i := range m.store.Project.Items {
		item := &m.store.Project.Items[i]
		if item.ContentType != "Issue" || item.Content.Number != msg.IssueNumber {
			continue
		}
		c := &item.Content
		if msg.IsClosed != nil {
			if *msg.IsClosed {
				c.State = "CLOSED"
			} else {
				c.State = "OPEN"
			}
		}
		if msg.Labels != nil {
			c.Labels.Nodes = msg.Labels.Nodes
		}
		if msg.NewComment != nil {
			c.Comments.Nodes = append(c.Comments.Nodes, *msg.NewComment)
			c.Comments.TotalCount++
		}
		if msg.AddedAssignees != nil {
			for _, a := range msg.AddedAssignees.Nodes {
				found := false
				for _, b := range c.Assignees.Nodes {
					found = found || a.Login == b.Login
				}
				if !found {
					c.Assignees.Nodes = append(c.Assignees.Nodes, a)
				}
			}
		}
		if msg.RemovedAssignees != nil {
			kept := c.Assignees.Nodes[:0]
			for _, a := range c.Assignees.Nodes {
				removed := false
				for _, r := range msg.RemovedAssignees.Nodes {
					removed = removed || a.Login == r.Login
				}
				if !removed {
					kept = append(kept, a)
				}
			}
			c.Assignees.Nodes = kept
		}
	}
}

// ---------------------------------------------------------------------------
// Field updates

// setField optimistically updates a field locally and sends the mutation.
// A nil value clears the field.
func (m *Model) setField(item *data.ProjectItem, field data.ProjectField,
	value *data.ProjectFieldValue, input map[string]any,
) tea.Cmd {
	var previous *data.ProjectFieldValue
	if v, ok := item.FieldValues[field.Name]; ok {
		previous = &v
	}
	if value == nil {
		delete(item.FieldValues, field.Name)
	} else {
		value.FieldName = field.Name
		item.FieldValues[field.Name] = *value
	}

	label := "item"
	if item.Content.Number > 0 {
		label = fmt.Sprintf("#%d", item.Content.Number)
	}
	newVal := "nothing"
	if value != nil {
		newVal = value.String()
	}
	m.store.Reindex(m.Ctx.Config)
	taskId := fmt.Sprintf("board_set_%s_%s_%d", item.Id, field.Id, time.Now().UnixNano())
	task := context.Task{
		Id:           taskId,
		StartText:    fmt.Sprintf("Setting %s of %s to %s", field.Name, label, newVal),
		FinishedText: fmt.Sprintf("%s of %s set to %s", field.Name, label, newVal),
		State:        context.TaskStart,
	}
	startCmd := m.Ctx.StartTask(task)
	projectId, itemId, fieldName, sectionId := m.store.Project.Id, item.Id, field.Name, m.Id
	update := data.ProjectFieldUpdate{
		ProjectId: projectId, ItemId: itemId, FieldId: field.Id, Value: input,
	}
	return tea.Batch(startCmd, func() tea.Msg {
		err := data.UpdateProjectItemField(update)
		return constants.TaskFinishedMsg{
			SectionId:   sectionId,
			SectionType: SectionType,
			TaskId:      taskId,
			Err:         err,
			Msg: fieldUpdatedMsg{
				itemId: itemId, fieldName: fieldName, previous: previous, err: err,
			},
		}
	})
}

func (m *Model) revertField(msg fieldUpdatedMsg) {
	item := m.findItem(msg.itemId)
	if item == nil {
		return
	}
	if msg.previous == nil {
		delete(item.FieldValues, msg.fieldName)
	} else {
		item.FieldValues[msg.fieldName] = *msg.previous
	}
	m.store.Reindex(m.Ctx.Config)
}

// MoveCard moves the selected card to the previous (-1) or next (+1) status.
func (m *Model) MoveCard(dir int) tea.Cmd {
	m.rebuild()
	item := m.currItem()
	field, ok := m.statusField()
	if item == nil || !ok || len(field.Options) == 0 {
		return nil
	}
	idx := -1
	if v, has := item.FieldValues[field.Name]; has {
		for i, o := range field.Options {
			if o.Id == v.OptionId {
				idx = i
			}
		}
	}
	target := idx + dir
	if idx == -1 && dir < 0 {
		return nil
	}
	if target < 0 || target >= len(field.Options) {
		return nil
	}
	opt := field.Options[target]
	m.follow = item.Id
	cmd := m.setField(item, field,
		&data.ProjectFieldValue{OptionId: opt.Id, Name: opt.Name},
		map[string]any{"singleSelectOptionId": opt.Id})
	m.rebuild()
	return cmd
}
