// Package reviewview is a full screen review mode for pull requests: browse
// the diff file by file, comment lines or ranges, then submit the review
// (approve, request changes or comment) in one go.
package reviewview

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/context"
)

type mode int

const (
	modeNormal mode = iota
	modeCompose
	modeSubmitChoice
	modeSubmitBody
	modeSubmitting
)

// PR identifies the pull request under review.
type PR struct {
	Repo   string // owner/name
	Number int
	Title  string
	Url    string
	Author string
}

// draft is a pending comment, kept until the review is submitted.
type draft struct {
	data.DraftReviewComment
}

// Pending comments survive leaving and reopening the review mode.
var pendingByPR = map[string][]draft{}

type event struct {
	name, label, hint string
}

var events = []event{
	{"COMMENT", "Comment", "Submit general feedback without explicit approval"},
	{"APPROVE", "Approve", "Submit feedback and approve merging these changes"},
	{"REQUEST_CHANGES", "Request changes", "Submit feedback that must be addressed before merging"},
}

type Model struct {
	ctx           *context.ProgramContext
	pr            PR
	width, height int

	loading bool
	err     error
	prId    string
	files   []fileDiff
	threads map[string][]data.PRReviewThread

	fileIdx  int
	cursor   map[int]int // line index per file
	offset   int         // first visible row of the diff
	selFrom  int         // start of the range selection, -1 when none
	showTree bool
	// selByShift is true for a selection made with shift+up/down: it ends
	// with the next plain move.
	selByShift bool
	headSha    string

	mode     mode
	editing  int // index of the pending comment being edited, -1 for a new one
	input    textarea.Model
	eventIdx int
	status   string
}

// ClosedMsg is sent when the review mode is left. Submitted is true when a
// review was sent: the PR should be fetched again.
type ClosedMsg struct {
	Submitted bool
}

type loadedMsg struct {
	files   []data.PRFile
	context data.PRReviewContext
	err     error
}

type submittedMsg struct {
	err error
}

func New(ctx *context.ProgramContext, pr PR, width, height int) (Model, tea.Cmd) {
	m := Model{
		ctx: ctx, pr: pr, width: width, height: height,
		loading: true, cursor: map[int]int{}, selFrom: -1, editing: -1,
		showTree: true,
	}
	repo, number := pr.Repo, pr.Number
	return m, func() tea.Msg {
		files, err := data.FetchPRFiles(repo, number)
		if err != nil {
			return loadedMsg{err: err}
		}
		rc, err := data.FetchPRReviewContext(repo, number)
		return loadedMsg{files: files, context: rc, err: err}
	}
}

func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
	m.input.SetWidth(m.inputWidth())
}

func (m *Model) pending() []draft {
	return pendingByPR[m.pr.Url]
}

func (m *Model) setPending(p []draft) {
	pendingByPR[m.pr.Url] = p
}

func (m *Model) currFile() *fileDiff {
	if m.fileIdx < 0 || m.fileIdx >= len(m.files) {
		return nil
	}
	return &m.files[m.fileIdx]
}

func (m *Model) currLine() int {
	return m.cursor[m.fileIdx]
}

func (m *Model) setLine(i int) {
	f := m.currFile()
	if f == nil {
		return
	}
	m.cursor[m.fileIdx] = max(0, min(i, len(f.lines)-1))
}

// selection returns the selected lines, in order.
func (m *Model) selection() (int, int) {
	cur := m.currLine()
	if m.selFrom < 0 {
		return cur, cur
	}
	return min(m.selFrom, cur), max(m.selFrom, cur)
}

func (m *Model) setFile(i int) {
	if i < 0 || i >= len(m.files) || i == m.fileIdx {
		return
	}
	m.fileIdx = i
	m.offset = 0
	m.selFrom, m.selByShift = -1, false
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case loadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.prId = msg.context.Id
		m.headSha = msg.context.HeadSha
		m.threads = map[string][]data.PRReviewThread{}
		for _, t := range msg.context.Threads {
			if !t.IsOutdated {
				m.threads[t.Path] = append(m.threads[t.Path], t)
			}
		}
		for _, f := range msg.files {
			m.files = append(m.files, newFileDiff(f))
		}
		sort.SliceStable(m.files, func(i, j int) bool { return m.files[i].Filename < m.files[j].Filename })
		// Start on the first changed line.
		for i, l := range m.files0Lines() {
			if l.kind == lineAdd || l.kind == lineDel {
				m.cursor[0] = i
				break
			}
		}
		return m, nil

	case contentMsg:
		if msg.fileIdx >= len(m.files) {
			return m, nil
		}
		f := &m.files[msg.fileIdx]
		f.content, f.contentErr = msg.lines, msg.err
		if msg.err != nil {
			m.status = "Could not load the file: " + msg.err.Error()
			return m, nil
		}
		if msg.fileIdx == m.fileIdx {
			m.expandAt(msg.lineIdx, msg.n)
		}
		return m, nil

	case submittedMsg:
		if msg.err != nil {
			m.mode = modeSubmitBody
			m.status = "Could not submit: " + msg.err.Error()
			return m, m.input.Focus()
		}
		m.setPending(nil)
		return m, func() tea.Msg { return ClosedMsg{Submitted: true} }

	case tea.KeyPressMsg:
		switch m.mode {
		case modeCompose:
			return m.updateCompose(msg)
		case modeSubmitChoice:
			return m.updateSubmitChoice(msg)
		case modeSubmitBody:
			return m.updateSubmitBody(msg)
		case modeSubmitting:
			return m, nil
		}
		return m.updateNormal(msg)
	}

	if m.mode == modeCompose || m.mode == modeSubmitBody {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) files0Lines() []diffLine {
	if len(m.files) == 0 {
		return nil
	}
	return m.files[0].lines
}

func (m Model) updateNormal(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	m.status = ""
	page := max(1, m.diffHeight()/2)
	k := msg.String()
	switch k {
	case "shift+down", "shift+up":
		if m.selFrom < 0 {
			if f := m.currFile(); f != nil && len(f.lines) > 0 {
				m.selFrom, m.selByShift = m.currLine(), true
			}
		}
		if k == "shift+down" {
			m.setLine(m.currLine() + 1)
		} else {
			m.setLine(m.currLine() - 1)
		}
		return m, nil
	case "j", "down", "k", "up", "ctrl+d", "pgdown", "ctrl+u", "pgup",
		"g", "home", "G", "end", "}", "{", "]", "tab", "J", "[", "shift+tab", "K":
		// A shift selection ends with the next plain move.
		if m.selByShift {
			m.selFrom, m.selByShift = -1, false
		}
	}
	switch k {
	case "q", "esc":
		if m.selFrom >= 0 && msg.String() == "esc" {
			m.selFrom, m.selByShift = -1, false
			return m, nil
		}
		return m, func() tea.Msg { return ClosedMsg{} }
	case "j", "down":
		m.setLine(m.currLine() + 1)
	case "k", "up":
		m.setLine(m.currLine() - 1)
	case "ctrl+d", "pgdown":
		m.setLine(m.currLine() + page)
	case "ctrl+u", "pgup":
		m.setLine(m.currLine() - page)
	case "g", "home":
		m.setLine(0)
	case "G", "end":
		m.setLine(1 << 30)
	case "}":
		m.jumpHunk(1)
	case "{":
		m.jumpHunk(-1)
	case "]", "tab", "J":
		m.setFile(m.fileIdx + 1)
	case "[", "shift+tab", "K":
		m.setFile(m.fileIdx - 1)
	case "t":
		m.showTree = !m.showTree
	case "v", "V":
		if m.selFrom >= 0 {
			m.selFrom, m.selByShift = -1, false
		} else if f := m.currFile(); f != nil && len(f.lines) > 0 {
			m.selFrom = m.currLine()
		}
	case "e":
		return m.startExpand(20)
	case "E":
		return m.startExpand(0)
	case "z":
		m.fold()
	case "Z":
		m.foldAll()
	case "c", "enter":
		return m.startCompose()
	case "x":
		m.deletePendingAtCursor()
	case "S", "s":
		m.mode = modeSubmitChoice
		m.status = ""
	}
	return m, nil
}

type contentMsg struct {
	fileIdx, lineIdx, n int
	lines               []string
	err                 error
}

// startExpand shows unchanged lines at the expander under the cursor,
// fetching the file first if needed. n <= 0 shows all of them.
func (m Model) startExpand(n int) (Model, tea.Cmd) {
	f := m.currFile()
	if f == nil || len(f.lines) == 0 {
		return m, nil
	}
	i := m.currLine()
	if !f.lines[i].isExpander() {
		m.status = "Move to a ⋯ line to show the unchanged lines there"
		return m, nil
	}
	if start, end := f.gap(i); end >= 0 && end < start {
		m.status = "No hidden line here"
		return m, nil
	}
	if f.content != nil {
		m.expandAt(i, n)
		return m, nil
	}
	if f.contentErr != nil || m.headSha == "" {
		m.status = "The file cannot be loaded"
		return m, nil
	}
	m.status = "Loading the file…"
	repo, path, sha, fileIdx := m.pr.Repo, f.Filename, m.headSha, m.fileIdx
	return m, func() tea.Msg {
		lines, err := data.FetchFileLines(repo, path, sha)
		return contentMsg{fileIdx: fileIdx, lineIdx: i, n: n, lines: lines, err: err}
	}
}

func (m *Model) expandAt(i, n int) {
	f := m.currFile()
	if f == nil || i >= len(f.lines) || !f.lines[i].isExpander() {
		return
	}
	m.status = ""
	m.selFrom, m.selByShift = -1, false
	before := len(f.lines)
	at := f.expand(i, n)
	switch {
	case at >= 0:
		// Stay on the expander, so that e goes on.
		m.setLine(at)
	default:
		// The tail is fully shown: land on its last line.
		m.setLine(i + len(f.lines) - before)
	}
	if len(f.lines) == before {
		m.status = "No hidden line here"
	}
}

func (m *Model) fold() {
	f := m.currFile()
	if f == nil || len(f.lines) == 0 {
		return
	}
	at := f.collapse(m.currLine())
	if at < 0 {
		m.status = "Nothing to fold here"
		return
	}
	m.selFrom, m.selByShift = -1, false
	m.setLine(at)
}

func (m *Model) foldAll() {
	f := m.currFile()
	if f == nil || len(f.lines) == 0 {
		return
	}
	// Keep the cursor on the same diff line.
	cur := f.lines[m.currLine()]
	f.collapseAll()
	m.selFrom, m.selByShift = -1, false
	idx := 0
	for i, l := range f.lines {
		if l.kind == cur.kind && l.oldNo == cur.oldNo && l.newNo == cur.newNo && l.text == cur.text {
			idx = i
			break
		}
		if cur.kind == lineExpanded && l.newNo > 0 && l.newNo > cur.newNo {
			idx = max(0, i-1)
			break
		}
	}
	m.setLine(idx)
}

func (m *Model) jumpHunk(dir int) {
	f := m.currFile()
	if f == nil {
		return
	}
	for i := m.currLine() + dir; i >= 0 && i < len(f.lines); i += dir {
		if f.lines[i].kind == lineHunk {
			m.setLine(i + 1)
			return
		}
	}
}

// pendingAt returns the index of the pending comment ending on line i of
// the current file, or -1.
func (m *Model) pendingAt(i int) int {
	f := m.currFile()
	if f == nil || i < 0 || i >= len(f.lines) {
		return -1
	}
	side, no := f.lines[i].position()
	for k, d := range m.pending() {
		if d.Path == f.Filename && d.Side == side && d.Line == no {
			return k
		}
	}
	return -1
}

func (m Model) startCompose() (Model, tea.Cmd) {
	f := m.currFile()
	if f == nil {
		return m, nil
	}
	from, to := m.selection()
	if !f.lines[to].commentable() {
		m.status = "Comments go on added, removed or unchanged lines"
		return m, nil
	}
	m.editing = m.pendingAt(to)
	m.input = m.newInput("Leave a comment (ctrl+s to save, esc to cancel)")
	if m.editing >= 0 {
		m.input.SetValue(m.pending()[m.editing].Body)
	}
	_ = from
	m.mode = modeCompose
	return m, m.input.Focus()
}

func (m *Model) inputWidth() int {
	return max(20, min(100, m.width-8))
}

func (m *Model) newInput(placeholder string) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = placeholder
	ta.ShowLineNumbers = false
	ta.SetWidth(m.inputWidth())
	ta.SetHeight(6)
	ta.CharLimit = 0
	return ta
}

func (m Model) updateCompose(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	case "ctrl+s", "ctrl+d":
		body := strings.TrimSpace(m.input.Value())
		m.mode = modeNormal
		m.input.Blur()
		m.saveDraft(body)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) saveDraft(body string) {
	f := m.currFile()
	if f == nil {
		return
	}
	pending := append([]draft(nil), m.pending()...)
	if m.editing >= 0 {
		if body == "" {
			pending = append(pending[:m.editing], pending[m.editing+1:]...)
		} else {
			pending[m.editing].Body = body
		}
		m.setPending(pending)
		m.selFrom, m.selByShift = -1, false
		return
	}
	if body == "" {
		return
	}
	from, to := m.selection()
	side, line := f.lines[to].position()
	d := draft{data.DraftReviewComment{Path: f.Filename, Line: line, Side: side, Body: body}}
	// A range starts on its first commentable line.
	for i := from; i < to; i++ {
		if f.lines[i].commentable() {
			d.StartSide, d.StartLine = f.lines[i].position()
			break
		}
	}
	m.setPending(append(pending, d))
	m.selFrom, m.selByShift = -1, false
}

func (m *Model) deletePendingAtCursor() {
	k := m.pendingAt(m.currLine())
	if k < 0 {
		m.status = "No pending comment on this line"
		return
	}
	pending := append([]draft(nil), m.pending()...)
	m.setPending(append(pending[:k], pending[k+1:]...))
	m.status = "Pending comment deleted"
}

func (m Model) updateSubmitChoice(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeNormal
	case "j", "down", "tab":
		m.eventIdx = (m.eventIdx + 1) % len(events)
	case "k", "up", "shift+tab":
		m.eventIdx = (m.eventIdx - 1 + len(events)) % len(events)
	case "1", "2", "3":
		m.eventIdx = int(msg.String()[0] - '1')
		fallthrough
	case "enter":
		m.mode = modeSubmitBody
		m.input = m.newInput("Leave a comment (optional for an approval)")
		m.status = ""
		return m, m.input.Focus()
	}
	return m, nil
}

func (m Model) updateSubmitBody(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeSubmitChoice
		m.input.Blur()
		return m, nil
	case "ctrl+s", "ctrl+d":
		return m.submit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) submit() (Model, tea.Cmd) {
	ev := events[m.eventIdx]
	body := strings.TrimSpace(m.input.Value())
	pending := m.pending()
	switch {
	case m.prId == "":
		m.status = "The pull request is not loaded"
		return m, nil
	case ev.name == "REQUEST_CHANGES" && body == "":
		m.status = "Requesting changes needs a comment"
		return m, nil
	case ev.name == "COMMENT" && body == "" && len(pending) == 0:
		m.status = "Write a comment, or add line comments first"
		return m, nil
	}
	comments := make([]data.DraftReviewComment, 0, len(pending))
	for _, d := range pending {
		comments = append(comments, d.DraftReviewComment)
	}
	m.mode = modeSubmitting
	m.input.Blur()
	m.status = fmt.Sprintf("Submitting the review (%s)…", strings.ToLower(ev.label))
	prId, name := m.prId, ev.name
	return m, func() tea.Msg {
		return submittedMsg{err: data.SubmitPRReview(prId, name, body, comments)}
	}
}

// IsTyping reports whether keys go to a text input.
func (m Model) IsTyping() bool {
	return m.mode == modeCompose || m.mode == modeSubmitBody
}
