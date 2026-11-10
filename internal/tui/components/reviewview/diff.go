package reviewview

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
)

type lineKind int

const (
	lineHunk lineKind = iota
	lineContext
	lineAdd
	lineDel
	lineNote     // "\ No newline at end of file"
	lineExpanded // unchanged line shown by expanding the context
	lineTail     // expander for the rest of the file, after the last hunk
)

// diffLine is one line of a file's unified diff.
type diffLine struct {
	kind    lineKind
	text    string
	oldNo   int // 0 when the line is not in the old file
	newNo   int // 0 when the line is not in the new file
	section string
	// hunkOld and hunkNew are the first line numbers of a hunk
	hunkOld, hunkNew int
}

func (l diffLine) isExpander() bool {
	return l.kind == lineHunk || l.kind == lineTail
}

// commentable reports whether GitHub accepts a comment on the line.
func (l diffLine) commentable() bool {
	return l.kind == lineContext || l.kind == lineAdd || l.kind == lineDel
}

// side and number of the line, as the review API wants them.
func (l diffLine) position() (string, int) {
	if l.kind == lineDel {
		return "LEFT", l.oldNo
	}
	return "RIGHT", l.newNo
}

type fileDiff struct {
	data.PRFile
	lines []diffLine
	// content is the file at the head of the PR, fetched to expand the
	// unchanged lines around the hunks.
	content    []string
	contentErr error
	// tailDelta is old minus new line numbers after the last hunk.
	tailDelta int
}

func newFileDiff(f data.PRFile) fileDiff {
	fd := fileDiff{PRFile: f}
	fd.lines, fd.tailDelta = parsePatch(f.Patch)
	if len(fd.lines) > 0 && f.Status != "removed" {
		fd.lines = append(fd.lines, diffLine{kind: lineTail})
	}
	return fd
}

// gap returns the new line numbers hidden at the expander on line i:
// between the line shown above it and the line shown below it (or the end
// of the file for the tail). end is -1 while the file content is unknown
// for the tail.
func (f *fileDiff) gap(i int) (start, end int) {
	prev := 0
	for j := i - 1; j >= 0; j-- {
		if f.lines[j].newNo > 0 {
			prev = f.lines[j].newNo
			break
		}
	}
	start = prev + 1
	if f.lines[i].kind == lineHunk {
		next := f.lines[i].hunkNew
		for j := i + 1; j < len(f.lines); j++ {
			if f.lines[j].newNo > 0 {
				next = f.lines[j].newNo
				break
			}
		}
		return start, next - 1
	}
	if f.content == nil {
		return start, -1
	}
	return start, len(f.content)
}

// expand shows up to n hidden lines at the expander on line i (all of them
// when n <= 0), like GitHub: above a hunk, the lines closest to it, the
// expander staying above them; at the tail, the next lines, the expander
// staying below them. It returns the new index of the expander, or -1 once
// the tail has nothing left to show.
func (f *fileDiff) expand(i, n int) int {
	l := f.lines[i]
	start, end := f.gap(i)
	if f.content == nil || end < start {
		if l.kind == lineTail && end >= 0 {
			f.lines = append(f.lines[:i], f.lines[i+1:]...)
			return -1
		}
		return i
	}
	if n <= 0 || n > end-start+1 {
		n = end - start + 1
	}
	from, delta, at := end-n+1, l.hunkOld-l.hunkNew, i+1
	if l.kind == lineTail {
		from, delta, at = start, f.tailDelta, i
	}
	inserted := make([]diffLine, 0, n)
	for no := from; no < from+n && no-1 < len(f.content); no++ {
		inserted = append(inserted, diffLine{
			kind: lineExpanded, text: f.content[no-1], newNo: no, oldNo: no + delta,
		})
	}
	f.lines = append(f.lines[:at], append(inserted, f.lines[at:]...)...)
	if l.kind == lineHunk {
		return i
	}
	tail := i + len(inserted)
	if from+n > end {
		// Nothing left after: drop the expander.
		f.lines = append(f.lines[:tail], f.lines[tail+1:]...)
		return -1
	}
	return tail
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@ ?(.*)$`)

func parsePatch(patch string) ([]diffLine, int) {
	if patch == "" {
		return nil, 0
	}
	var lines []diffLine
	oldNo, newNo := 0, 0
	for _, raw := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		if m := hunkRe.FindStringSubmatch(raw); m != nil {
			oldNo, _ = strconv.Atoi(m[1])
			newNo, _ = strconv.Atoi(m[2])
			lines = append(lines, diffLine{
				kind: lineHunk, text: raw, section: m[3], hunkOld: oldNo, hunkNew: newNo,
			})
			continue
		}
		if raw == "" {
			raw = " "
		}
		switch raw[0] {
		case '+':
			lines = append(lines, diffLine{kind: lineAdd, text: raw[1:], newNo: newNo})
			newNo++
		case '-':
			lines = append(lines, diffLine{kind: lineDel, text: raw[1:], oldNo: oldNo})
			oldNo++
		case '\\':
			lines = append(lines, diffLine{kind: lineNote, text: raw})
		default:
			lines = append(lines, diffLine{kind: lineContext, text: raw[1:], oldNo: oldNo, newNo: newNo})
			oldNo++
			newNo++
		}
	}
	return lines, oldNo - newNo
}
