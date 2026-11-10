package reviewview

import (
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
	lines := parsePatch(patch)
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
		files:   []fileDiff{{PRFile: data.PRFile{Filename: "main.go"}, lines: parsePatch(patch)}},
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
