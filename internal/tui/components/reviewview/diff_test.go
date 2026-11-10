package reviewview

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/config"
	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/context"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/theme"
)

const patch = `@@ -10,4 +10,5 @@ func main() {
 a
-b
+B
+C
 d
\ No newline at end of file`

func TestParsePatch(t *testing.T) {
	lines, _ := parsePatch(patch)
	want := []struct {
		kind         lineKind
		oldNo, newNo int
	}{
		{lineHunk, 0, 0},
		{lineContext, 10, 10},
		{lineDel, 11, 0},
		{lineAdd, 0, 11},
		{lineAdd, 0, 12},
		{lineContext, 12, 13},
		{lineNote, 0, 0},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines", len(lines))
	}
	for i, w := range want {
		l := lines[i]
		if l.kind != w.kind || l.oldNo != w.oldNo || l.newNo != w.newNo {
			t.Errorf("line %d: got %+v, want %+v", i, l, w)
		}
	}
	if side, no := lines[2].position(); side != "LEFT" || no != 11 {
		t.Errorf("deleted line: %s %d", side, no)
	}
	if side, no := lines[4].position(); side != "RIGHT" || no != 12 {
		t.Errorf("added line: %s %d", side, no)
	}
}

func TestDraftRange(t *testing.T) {
	m := Model{
		pr:      PR{Url: "test://pr"},
		files:   []fileDiff{newFileDiff(data.PRFile{Filename: "main.go", Patch: patch})},
		cursor:  map[int]int{},
		selFrom: 0, // from the hunk header...
		editing: -1,
	}
	m.cursor[0] = 4 // ...to the second added line
	m.saveDraft("nice")
	p := m.pending()
	if len(p) != 1 {
		t.Fatalf("pending: %+v", p)
	}
	d := p[0]
	// The range starts on the first commentable line, not the hunk header.
	if d.Path != "main.go" || d.Line != 12 || d.Side != "RIGHT" ||
		d.StartLine != 10 || d.StartSide != "RIGHT" {
		t.Errorf("draft: %+v", d)
	}
	if k := m.pendingAt(4); k != 0 {
		t.Errorf("pendingAt: %d", k)
	}
	m.deletePendingAtCursor()
	if len(m.pending()) != 0 {
		t.Errorf("not deleted")
	}
}

func TestExpand(t *testing.T) {
	// Lines 1..20 of the new file; the hunk shows 10..13, the tail is 14..20.
	content := make([]string, 20)
	for i := range content {
		content[i] = fmt.Sprintf("line %d", i+1)
	}
	f := newFileDiff(data.PRFile{Filename: "main.go", Patch: patch})
	f.content = content
	if start, end := f.gap(0); start != 1 || end != 9 {
		t.Fatalf("gap above the hunk: %d..%d", start, end)
	}
	// Show 3 lines above the hunk: 7, 8, 9 (old 7, 8, 9), under the
	// expander, which stays on top.
	if at := f.expand(0, 3); at != 0 {
		t.Fatalf("expander moved to %d", at)
	}
	if l := f.lines[1]; l.kind != lineExpanded || l.newNo != 7 || l.oldNo != 7 {
		t.Errorf("first expanded line: %+v", l)
	}
	if start, end := f.gap(0); start != 1 || end != 6 {
		t.Errorf("remaining gap: %d..%d", start, end)
	}
	f.expand(0, 0)
	if start, end := f.gap(0); end >= start {
		t.Errorf("gap left after expanding all: %d..%d", start, end)
	}
	// The tail: after new line 13 (old 12), lines 14..20 with old = new - 1.
	tail := len(f.lines) - 1
	if f.lines[tail].kind != lineTail {
		t.Fatalf("no tail expander")
	}
	before := len(f.lines)
	if at := f.expand(tail, 0); at != -1 || len(f.lines) != before+6 {
		t.Fatalf("tail: at %d, %d lines instead of %d", at, len(f.lines), before+6)
	}
	last := f.lines[len(f.lines)-1]
	if last.kind != lineExpanded || last.newNo != 20 || last.oldNo != 19 {
		t.Errorf("last line: %+v", last)
	}
}

func TestCollapse(t *testing.T) {
	content := make([]string, 20)
	for i := range content {
		content[i] = fmt.Sprintf("line %d", i+1)
	}
	f := newFileDiff(data.PRFile{Filename: "main.go", Patch: patch})
	f.content = content
	original := len(f.lines)

	// Fold from inside a block above the hunk: back to the hunk expander.
	f.expand(0, 3)
	if at := f.collapse(2); at != 0 || len(f.lines) != original {
		t.Fatalf("collapse above hunk: at %d, %d lines", at, len(f.lines))
	}
	// Fold the fully shown tail from its last line: the tail comes back.
	f.expand(len(f.lines)-1, 0)
	if f.lines[len(f.lines)-1].kind == lineTail {
		t.Fatalf("tail still there")
	}
	at := f.collapse(len(f.lines) - 1)
	if at < 0 || f.lines[at].kind != lineTail || len(f.lines) != original {
		t.Fatalf("collapse tail: at %d, %d lines", at, len(f.lines))
	}
	// Nothing to fold on a changed line.
	if at := f.collapse(3); at != -1 {
		t.Errorf("collapse on a changed line: %d", at)
	}
	// Fold everything.
	f.expand(0, 2)
	f.expand(len(f.lines)-1, 2)
	f.collapseAll()
	if len(f.lines) != original || f.lines[len(f.lines)-1].kind != lineTail {
		t.Errorf("collapseAll: %d lines", len(f.lines))
	}
}

func TestBuildTree(t *testing.T) {
	rows := buildTree([]string{
		"app/src/main/java/com/android/shifter/ui/theme/Theme.kt",
		"app/src/main/AndroidManifest.xml",
		"app/src/main/java/com/android/shifter/MainActivity.kt",
		"app/src/main/java/com/android/shifter/ui/components/ShifterTopBar.kt",
		"app/src/test/java/com/android/shifter/ui/theme/EdgeToEdgeTest.kt",
	})
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprintf("%d:%s", r.depth, r.name))
	}
	want := []string{
		"0:app/src",
		"1:main",
		"2:java/com/android/shifter",
		"3:ui",
		"4:components",
		"5:ShifterTopBar.kt",
		"4:theme",
		"5:Theme.kt",
		"3:MainActivity.kt",
		"2:AndroidManifest.xml",
		"1:test/java/com/android/shifter/ui/theme",
		"2:EdgeToEdgeTest.kt",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("tree:\n got %v\nwant %v", got, want)
	}
}

func TestReply(t *testing.T) {
	ctx := &context.ProgramContext{Config: &config.Config{}, Theme: *theme.DefaultTheme}
	m := Model{ctx: ctx, pr: PR{Url: "test://reply"}, cursor: map[int]int{}, selFrom: -1,
		editing: -1, width: 120, height: 40}
	m.files = []fileDiff{newFileDiff(data.PRFile{Filename: "main.go", Patch: patch})}
	m.threads = map[string][]data.PRReviewThread{"main.go": {{
		Id: "T1", Path: "main.go", Line: 12, DiffSide: "RIGHT",
		Comments: []data.PRReviewThreadComment{{Author: "bob", Body: "Why?"}},
	}}}

	m.cursor[0] = 1 // new line 10: no conversation
	m, _ = m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if m.mode != modeNormal || m.status == "" {
		t.Fatalf("reply without conversation: mode %d", m.mode)
	}
	m.cursor[0] = 4 // new line 12: bob's conversation
	m, _ = m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if m.mode != modeReply || m.replyTo != 0 {
		t.Fatalf("reply: mode %d replyTo %d", m.mode, m.replyTo)
	}
	if !strings.Contains(m.renderDialog(), "@bob") {
		t.Errorf("dialog does not show who is answered")
	}
	// The reply shows once sent.
	m, _ = m.Update(replySentMsg{path: "main.go", index: 0,
		comment: data.PRReviewThreadComment{Author: "me", Body: "Because"}})
	if m.mode != modeNormal || len(m.threads["main.go"][0].Comments) != 2 {
		t.Errorf("after sending: mode %d, %d comments", m.mode, len(m.threads["main.go"][0].Comments))
	}
}
