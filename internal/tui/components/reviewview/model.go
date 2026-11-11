// Package reviewview is a full screen review mode for pull requests: browse
// the diff file by file, comment lines or ranges, then submit the review
// (approve, request changes or comment) in one go.
package reviewview

import (
	"fmt"
	"sort"
	"strings"
	"time"

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
	modeReply
	modeReplying
	modePickCommit
)

// PR identifies the pull request under review.
type PR struct {
	Repo   string // owner/name
	Number int
	Title  string
	Url    string
	Author string
	// Local is set to review a local worktree instead: its diff is read
	// with git, and Number is its pull request, if any (0 otherwise).
	Local *Local
}

// Local is a worktree under review. Its comments are local notes.
type Local struct {
	Dir       string
	Branch    string
	Base      string // shown, e.g. origin/main
	MergeBase string
	// Uncommitted reviews the uncommitted changes only (diff against HEAD)
	// instead of the whole branch since the merge base.
	Uncommitted bool
	// Commit reviews a single commit instead, when set.
	Commit, Subject string
	Commits         []data.WorktreeCommit
}

func (l *Local) meta() data.NotesMeta {
	return data.NotesMeta{Repo: "", Branch: l.Branch, Dir: l.Dir, Base: l.Base}
}

// draft is a pending comment, kept until the review is submitted. For a
// worktree, it is a local note and never leaves the machine.
type draft struct {
	data.LocalNote
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
	tree    []treeRow
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
	// loadSeq numbers the loads of a worktree, to drop stale results.
	loadSeq int

	mode    mode
	editing int // index of the pending comment being edited, -1 for a new one
	replyTo int // index of the conversation replied to, in m.threads[path]
	// replyNote is the index in pending() of the local note replied to.
	replyNote int
	input     textarea.Model
	eventIdx  int
	pickIdx   int
	// viewed maps the files marked as viewed to their patch's hash.
	viewed map[string]string
	status string
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
	seq     int // for a worktree
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
	m.viewed = data.LoadViewed(m.pendingKey())
	if pr.Local != nil {
		// Agents may have replied since: read the notes again.
		delete(pendingByPR, m.pendingKey())
	}
	cmd := m.load()
	return m, cmd
}

func (m *Model) load() tea.Cmd {
	repo, number := m.pr.Repo, m.pr.Number
	if l := m.pr.Local; l != nil {
		m.loadSeq++
		dir, seq := l.Dir, m.loadSeq
		commit, base := l.Commit, l.MergeBase
		if l.Uncommitted {
			base = "HEAD"
		}
		return func() tea.Msg {
			var files []data.PRFile
			var err error
			if commit != "" {
				files, err = data.CommitDiffFiles(dir, commit)
			} else {
				files, err = data.LocalDiffFiles(dir, base)
			}
			return loadedMsg{files: files, err: err, seq: seq}
		}
	}
	return func() tea.Msg {
		files, err := data.FetchPRFiles(repo, number)
		if err != nil {
			return loadedMsg{err: err}
		}
		rc, err := data.FetchPRReviewContext(repo, number)
		return loadedMsg{files: files, context: rc, err: err}
	}
}

// pendingKey identifies the pending comments: per PR, or per worktree
// branch for local notes.
func (m *Model) pendingKey() string {
	if l := m.pr.Local; l != nil {
		return "local:" + m.pr.Repo + ":" + l.Branch
	}
	return m.pr.Url
}

// toggleUncommitted switches a worktree review between the whole branch
// and the uncommitted changes only.
func (m Model) toggleUncommitted() (Model, tea.Cmd) {
	if m.pr.Local == nil {
		return m, nil
	}
	return m.setScope(!m.pr.Local.Uncommitted, "", "")
}

// setScope shows the whole branch, the uncommitted changes or one commit.
func (m Model) setScope(uncommitted bool, commit, subject string) (Model, tea.Cmd) {
	l := *m.pr.Local
	l.Uncommitted, l.Commit, l.Subject = uncommitted, commit, subject
	m.pr.Local = &l
	m.loading, m.err = true, nil
	m.files, m.tree, m.threads = nil, nil, nil
	m.fileIdx, m.offset = 0, 0
	m.cursor = map[int]int{}
	m.selFrom, m.selByShift = -1, false
	cmd := m.load()
	return m, cmd
}

func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
	m.input.SetWidth(m.inputWidth())
	m.clampOffset()
}

// mergeReplies keeps the replies on disk and adds the new ones, in time
// order.
func mergeReplies(disk, mine []data.NoteReply) []data.NoteReply {
	res := append([]data.NoteReply(nil), disk...)
	for _, r := range mine {
		found := false
		for _, d := range disk {
			if d.Author == r.Author && d.CreatedAt.Equal(r.CreatedAt) && d.Body == r.Body {
				found = true
				break
			}
		}
		if !found {
			res = append(res, r)
		}
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res
}

// allPending is every pending comment; for a worktree, the notes of all
// its diffs (whole branch and single commits).
func (m *Model) allPending() []draft {
	key := m.pendingKey()
	p, ok := pendingByPR[key]
	if !ok && m.pr.Local != nil {
		for _, n := range data.LoadLocalNotes(m.pr.Repo, m.pr.Local.Branch) {
			p = append(p, draft{n})
		}
		pendingByPR[key] = p
	}
	return p
}

// pending is the comments of the diff shown: a worktree's notes are tied to
// the working tree or to one commit.
func (m *Model) pending() []draft {
	all := m.allPending()
	l := m.pr.Local
	if l == nil {
		return all
	}
	var p []draft
	for _, d := range all {
		if d.Commit == l.Commit {
			p = append(p, d)
		}
	}
	return p
}

func (m *Model) setPending(p []draft) {
	key := m.pendingKey()
	l := m.pr.Local
	if l == nil {
		pendingByPR[key] = p
		return
	}
	var all []draft
	for _, d := range m.allPending() {
		if d.Commit != l.Commit {
			all = append(all, d)
		}
	}
	for _, d := range p {
		d.Commit, d.Subject = l.Commit, l.Subject
		all = append(all, d)
	}
	// Keep the replies written meanwhile by agents.
	disk := map[int]data.LocalNote{}
	for _, n := range data.LoadLocalNotes(m.pr.Repo, l.Branch) {
		disk[n.Id] = n
	}
	for i := range all {
		if d, ok := disk[all[i].Id]; ok {
			all[i].Replies = mergeReplies(d.Replies, all[i].Replies)
		}
	}
	pendingByPR[key] = all
	notes := make([]data.LocalNote, 0, len(all))
	for _, d := range all {
		notes = append(notes, d.LocalNote)
	}
	meta := l.meta()
	meta.Repo = m.pr.Repo
	if err := data.SaveLocalNotes(meta, notes); err != nil {
		m.status = "Could not save the notes: " + err.Error()
	}
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
	m, cmd := m.update(msg)
	// Scroll only when the cursor leaves the screen, and remember it: the
	// screen then stays put while the cursor moves within it.
	m.clampOffset()
	return m, cmd
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case loadedMsg:
		if m.pr.Local != nil && msg.seq != m.loadSeq {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.threads = map[string][]data.PRReviewThread{}
		if m.pr.Local == nil {
			m.applyPRContext(msg.context)
		}
		for _, f := range msg.files {
			m.files = append(m.files, newFileDiff(f))
		}
		m.orderFilesLikeTree()
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

	case replySentMsg:
		if msg.err != nil {
			m.mode = modeReply
			m.status = "Could not send the reply: " + msg.err.Error()
			return m, m.input.Focus()
		}
		threads := m.threads[msg.path]
		if msg.index < len(threads) {
			threads[msg.index].Comments = append(threads[msg.index].Comments, msg.comment)
		}
		m.mode = modeNormal
		m.status = "Reply sent"
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
		case modeSubmitting, modeReplying:
			return m, nil
		case modeReply:
			return m.updateReply(msg)
		case modePickCommit:
			return m.updatePickCommit(msg)
		}
		return m.updateNormal(msg)
	}

	if m.IsTyping() {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// applyPRContext keeps what the review of the pull request needs.
func (m *Model) applyPRContext(rc data.PRReviewContext) {
	m.prId = rc.Id
	m.headSha = rc.HeadSha
	m.threads = map[string][]data.PRReviewThread{}
	for _, t := range rc.Threads {
		if !t.IsOutdated {
			m.threads[t.Path] = append(m.threads[t.Path], t)
		}
	}
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
	case "r":
		return m.startReply()
	case "x":
		m.deletePendingAtCursor()
	case "m":
		m.toggleViewed()
	case "f":
		m.toggleNoteFold()
	case "u":
		return m.toggleUncommitted()
	case "C":
		m.OpenCommitPicker()
	case "S", "s":
		if reason := m.cannotSubmit(); reason != "" {
			m.status = reason
			return m, nil
		}
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
	if l := m.pr.Local; l != nil {
		lines, err := data.LocalFileLines(l.Dir, f.Filename)
		if l.Commit != "" {
			lines, err = data.CommitFileLines(l.Dir, l.Commit, f.Filename)
		}
		f.content, f.contentErr = lines, err
		if err != nil {
			m.status = "Could not read the file: " + err.Error()
			return m, nil
		}
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

type replySentMsg struct {
	path    string
	index   int
	comment data.PRReviewThreadComment
	err     error
}

// threadAt returns the index, in m.threads[path], of the conversation shown
// under line i of the current file, or -1.
func (m *Model) threadAt(i int) int {
	f := m.currFile()
	if f == nil || i < 0 || i >= len(f.lines) || !f.lines[i].commentable() {
		return -1
	}
	side, no := f.lines[i].position()
	for k, t := range m.threads[f.Filename] {
		tSide := t.DiffSide
		if tSide == "" {
			tSide = "RIGHT"
		}
		if t.Line == no && tSide == side {
			return k
		}
	}
	return -1
}

// isReplyingTo reports whether the reply box is open on that conversation.
func (m *Model) isReplyingTo(path string, k int) bool {
	f := m.currFile()
	return (m.mode == modeReply || m.mode == modeReplying) && f != nil &&
		f.Filename == path && m.replyTo == k
}

// nearestNote returns the line of the current file carrying the local note
// closest to the cursor (below it on a tie), or -1.
func (m *Model) nearestNote() int {
	f := m.currFile()
	if f == nil {
		return -1
	}
	cur, best := m.currLine(), -1
	for i := range f.lines {
		if !f.lines[i].commentable() || m.pendingAt(i) < 0 {
			continue
		}
		if best < 0 || abs(i-cur) < abs(best-cur) || (abs(i-cur) == abs(best-cur) && i > best) {
			best = i
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// startNoteReply opens the reply box under the local note closest to the
// cursor: the discussion with the agents.
func (m Model) startNoteReply() (Model, tea.Cmd) {
	line := m.nearestNote()
	if line < 0 {
		m.status = "No note in this file"
		return m, nil
	}
	m.selFrom, m.selByShift = -1, false
	m.setLine(line)
	m.replyNote = m.pendingAt(line)
	if p := m.pending(); p[m.replyNote].Folded {
		p = append([]draft(nil), p...)
		p[m.replyNote].Folded = false
		m.setPending(p)
	}
	m.input = m.newInput("Write a reply")
	m.input.SetWidth(max(10, m.cardWidth()-6))
	m.input.SetHeight(4)
	m.status = ""
	m.mode = modeReply
	return m, m.input.Focus()
}

func (m *Model) saveNoteReply(body string) {
	p := append([]draft(nil), m.pending()...)
	m.mode = modeNormal
	m.input.Blur()
	if m.replyNote < 0 || m.replyNote >= len(p) {
		return
	}
	author := m.ctx.User
	if author == "" {
		author = "me"
	}
	d := &p[m.replyNote]
	d.Replies = append(append([]data.NoteReply(nil), d.Replies...),
		data.NoteReply{Author: author, Body: body, CreatedAt: time.Now()})
	m.setPending(p)
	m.status = "Reply saved"
}

// toggleNoteFold folds the discussion of the note closest to the cursor:
// folded, it is closed, and agents leave it alone.
func (m *Model) toggleNoteFold() {
	if m.pr.Local == nil {
		return
	}
	line := m.nearestNote()
	if line < 0 {
		m.status = "No note in this file"
		return
	}
	m.setLine(line)
	k := m.pendingAt(line)
	p := append([]draft(nil), m.pending()...)
	p[k].Folded = !p[k].Folded
	m.setPending(p)
	if p[k].Folded {
		m.status = fmt.Sprintf("Note %d folded: closed", p[k].Id)
	} else {
		m.status = fmt.Sprintf("Note %d unfolded: open again", p[k].Id)
	}
}

// isReplyingToNote reports whether the reply box is open on that note.
func (m *Model) isReplyingToNote(k int) bool {
	return m.pr.Local != nil && m.mode == modeReply && m.replyNote == k
}

// nearestThread returns the line of the current file carrying the
// conversation closest to the cursor (below it on a tie), or -1.
func (m *Model) nearestThread() int {
	f := m.currFile()
	if f == nil {
		return -1
	}
	cur, best := m.currLine(), -1
	for i := range f.lines {
		if m.threadAt(i) < 0 {
			continue
		}
		d, bd := i-cur, best-cur
		if d < 0 {
			d = -d
		}
		if bd < 0 {
			bd = -bd
		}
		if best < 0 || d < bd || (d == bd && i > best) {
			best = i
		}
	}
	return best
}

// startReply opens the reply box under the conversation closest to the
// cursor, moving the cursor to it.
func (m Model) startReply() (Model, tea.Cmd) {
	if m.pr.Local != nil {
		return m.startNoteReply()
	}
	line := m.nearestThread()
	if line < 0 {
		m.status = "No conversation in this file"
		return m, nil
	}
	m.selFrom, m.selByShift = -1, false
	m.setLine(line)
	m.replyTo = m.threadAt(line)
	m.input = m.newInput("Write a reply")
	m.input.SetWidth(max(10, m.cardWidth()-6))
	m.input.SetHeight(4)
	m.status = ""
	m.mode = modeReply
	return m, m.input.Focus()
}

func (m Model) updateReply(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.status = ""
		m.input.Blur()
		return m, nil
	case "ctrl+s", "ctrl+d":
		body := strings.TrimSpace(m.input.Value())
		if body == "" {
			m.status = "The reply is empty"
			return m, nil
		}
		if m.pr.Local != nil {
			m.saveNoteReply(body)
			return m, nil
		}
		f := m.currFile()
		threads := m.threads[f.Filename]
		if m.replyTo >= len(threads) {
			m.mode = modeNormal
			return m, nil
		}
		m.mode = modeReplying
		m.status = "Sending the reply…"
		m.input.Blur()
		path, index, id := f.Filename, m.replyTo, threads[m.replyTo].Id
		return m, func() tea.Msg {
			c, err := data.ReplyToReviewThread(id, body)
			return replySentMsg{path: path, index: index, comment: c, err: err}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
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
	notes := make([]data.LocalNote, 0, len(m.allPending()))
	for _, x := range m.allPending() {
		notes = append(notes, x.LocalNote)
	}
	d := draft{data.LocalNote{
		Id:                 data.NextNoteId(notes),
		DraftReviewComment: data.DraftReviewComment{Path: f.Filename, Line: line, Side: side, Body: body},
		Excerpt:            f.excerpt(from, to),
		CreatedAt:          time.Now(),
	}}
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

func (m *Model) isViewed(f *fileDiff) bool {
	h, ok := m.viewed[f.Filename]
	return ok && h == data.PatchHash(f.Patch)
}

func (m *Model) viewedCount() int {
	n := 0
	for i := range m.files {
		if m.isViewed(&m.files[i]) {
			n++
		}
	}
	return n
}

// toggleViewed marks the current file as viewed, or not, like GitHub's
// checkbox; once viewed, the next file not viewed yet is shown.
func (m *Model) toggleViewed() {
	f := m.currFile()
	if f == nil {
		return
	}
	viewed := make(map[string]string, len(m.viewed)+1)
	for k, v := range m.viewed {
		viewed[k] = v
	}
	if m.isViewed(f) {
		delete(viewed, f.Filename)
		m.status = "Marked as not viewed"
	} else {
		viewed[f.Filename] = data.PatchHash(f.Patch)
		m.status = "Marked as viewed"
	}
	m.viewed = viewed
	if err := data.SaveViewed(m.pendingKey(), viewed); err != nil {
		m.status = "Could not save: " + err.Error()
	}
	if !m.isViewed(f) {
		return
	}
	// The tree order is the files' order.
	for i := 1; i < len(m.files); i++ {
		j := (m.fileIdx + i) % len(m.files)
		if !m.isViewed(&m.files[j]) {
			m.setFile(j)
			return
		}
	}
	m.status = "All files viewed"
}

// scopeChoice is an entry of the commit picker.
type scopeChoice struct {
	label, hint     string
	uncommitted     bool
	commit, subject string
}

func (m *Model) scopeChoices() []scopeChoice {
	l := m.pr.Local
	choices := []scopeChoice{
		{label: "All changes", hint: "the whole branch since " + l.Base + ", uncommitted changes included"},
		{label: "Uncommitted changes", hint: "since the last commit", uncommitted: true},
	}
	for _, c := range l.Commits {
		choices = append(choices, scopeChoice{label: c.Sha + " " + c.Subject, hint: "commit", commit: c.Sha, subject: c.Subject})
	}
	return choices
}

func (m *Model) currentScope() int {
	l := m.pr.Local
	for i, c := range m.scopeChoices() {
		if c.commit == l.Commit && (l.Commit != "" || c.uncommitted == l.Uncommitted) {
			return i
		}
	}
	return 0
}

// OpenCommitPicker shows the list of diffs a worktree can be reviewed by.
func (m *Model) OpenCommitPicker() {
	if m.pr.Local == nil {
		return
	}
	m.pickIdx = m.currentScope()
	m.mode = modePickCommit
	m.status = ""
}

func (m Model) updatePickCommit(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	n := len(m.scopeChoices())
	switch msg.String() {
	case "esc", "q", "C":
		m.mode = modeNormal
	case "j", "down", "tab":
		m.pickIdx = (m.pickIdx + 1) % n
	case "k", "up", "shift+tab":
		m.pickIdx = (m.pickIdx - 1 + n) % n
	case "g", "home":
		m.pickIdx = 0
	case "G", "end":
		m.pickIdx = n - 1
	case "enter":
		m.mode = modeNormal
		c := m.scopeChoices()[m.pickIdx]
		if m.pickIdx == m.currentScope() {
			return m, nil
		}
		return m.setScope(c.uncommitted, c.commit, c.subject)
	}
	return m, nil
}

// cannotSubmit explains why a worktree review cannot be sent to GitHub:
// its comments then stay as local notes.
func (m *Model) cannotSubmit() string {
	if m.pr.Local == nil {
		return ""
	}
	return "Local notes, never sent: " + m.NotesFile()
}

// NotesFile is the Markdown file of a worktree's notes.
func (m *Model) NotesFile() string {
	if m.pr.Local == nil {
		return ""
	}
	return data.NotesFile(m.pr.Repo, m.pr.Local.Branch)
}

// IsTyping reports whether keys go to a text input.
func (m Model) IsTyping() bool {
	return m.mode == modeCompose || m.mode == modeSubmitBody || m.mode == modeReply
}
