package reviewview

import (
	"fmt"
	"testing"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
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
