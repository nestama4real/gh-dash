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
	lineNote // "\ No newline at end of file"
)

// diffLine is one line of a file's unified diff.
type diffLine struct {
	kind    lineKind
	text    string
	oldNo   int // 0 when the line is not in the old file
	newNo   int // 0 when the line is not in the new file
	section string
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
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@ ?(.*)$`)

func parsePatch(patch string) []diffLine {
	if patch == "" {
		return nil
	}
	var lines []diffLine
	oldNo, newNo := 0, 0
	for _, raw := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		if m := hunkRe.FindStringSubmatch(raw); m != nil {
			oldNo, _ = strconv.Atoi(m[1])
			newNo, _ = strconv.Atoi(m[2])
			lines = append(lines, diffLine{kind: lineHunk, text: raw, section: m[3]})
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
	return lines
}
