// Package images shows the images of issue and PR descriptions inline, with
// kitty's graphics protocol and its Unicode placeholders: the image is sent
// to the terminal once, then drawn wherever special placeholder characters
// are printed. Placeholders are plain text cells, so they scroll and redraw
// with the rest of the TUI.
//
// See https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders
package images

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const placeholder = '\U0010EEEE'

// Config of inline images, in terminal cells.
type Config struct {
	Enabled   bool
	MaxWidth  int
	MaxHeight int
}

type state int

const (
	statePending state = iota
	stateLoading
	stateReady
	stateFailed
)

type entry struct {
	url   string
	state state
	err   error
	// pixel size and PNG data, scaled down for the terminal
	pxW, pxH int
	png      []byte
	// file is the original image in the cache, for the full screen viewer
	file string
	// id is the kitty image id (1..255, encoded in the 256-color foreground
	// of the placeholders); 0 when not transmitted to the terminal.
	id         int
	placedCols int
	placedRows int
}

var reg = struct {
	sync.Mutex
	cfg     Config
	entries map[string]*entry
	byId    [256]*entry
	nextId  int
	toFetch []*entry
	raw     strings.Builder
	faint   lipgloss.Style
}{entries: map[string]*entry{}, nextId: 1}

// Configure sets the limits. Images stay disabled outside kitty-compatible
// terminals.
func Configure(cfg Config, faint lipgloss.Style) {
	reg.Lock()
	defer reg.Unlock()
	if cfg.MaxWidth <= 0 {
		cfg.MaxWidth = 80
	}
	if cfg.MaxHeight <= 0 {
		cfg.MaxHeight = 20
	}
	cfg.Enabled = cfg.Enabled && Supported()
	reg.cfg = cfg
	reg.faint = faint
}

// Supported reports whether the terminal draws Unicode image placeholders.
func Supported() bool {
	if os.Getenv("TMUX") != "" {
		return false
	}
	return os.Getenv("KITTY_WINDOW_ID") != "" ||
		strings.Contains(os.Getenv("TERM"), "kitty") ||
		os.Getenv("TERM_PROGRAM") == "ghostty"
}

// Ref is an image found in a markdown body.
type Ref struct {
	Alt string
	Url string
}

var (
	mdImageRe   = regexp.MustCompile(`!\[([^\]]*)\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	htmlImageRe = regexp.MustCompile(`(?i)<img\b[^>]*>`)
	srcAttrRe   = regexp.MustCompile(`(?i)\bsrc\s*=\s*["']([^"']+)["']`)
	altAttrRe   = regexp.MustCompile(`(?i)\balt\s*=\s*["']([^"']*)["']`)
	markerRe    = regexp.MustCompile(`GHDASHIMG(\d+)X`)
)

// FindImages lists the images of a markdown body, in order.
func FindImages(body string) []Ref {
	_, refs := extract(body)
	return refs
}

// extract replaces each image with a marker paragraph.
func extract(body string) (string, []Ref) {
	var refs []Ref
	type match struct {
		start, end int
		ref        Ref
	}
	var matches []match
	for _, m := range mdImageRe.FindAllStringSubmatchIndex(body, -1) {
		matches = append(matches, match{m[0], m[1], Ref{
			Alt: body[m[2]:m[3]], Url: body[m[4]:m[5]],
		}})
	}
	for _, m := range htmlImageRe.FindAllStringIndex(body, -1) {
		tag := body[m[0]:m[1]]
		src := srcAttrRe.FindStringSubmatch(tag)
		if src == nil {
			continue
		}
		ref := Ref{Url: src[1]}
		if alt := altAttrRe.FindStringSubmatch(tag); alt != nil {
			ref.Alt = alt[1]
		}
		matches = append(matches, match{m[0], m[1], ref})
	}
	if len(matches) == 0 {
		return body, nil
	}
	// Keep document order.
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0 && matches[j].start < matches[j-1].start; j-- {
			matches[j], matches[j-1] = matches[j-1], matches[j]
		}
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		if m.start < last {
			continue // overlapping matches
		}
		b.WriteString(body[last:m.start])
		fmt.Fprintf(&b, "\n\nGHDASHIMG%dX\n\n", len(refs))
		refs = append(refs, m.ref)
		last = m.end
	}
	b.WriteString(body[last:])
	return b.String(), refs
}

// Renderer is the subset of glamour's renderer used here.
type Renderer interface {
	Render(string) (string, error)
}

// RenderMarkdown renders a markdown body with its images inline, fitting in
// width cells. Images that are not loaded yet are requested and show a
// one-line placeholder until they are.
func RenderMarkdown(r Renderer, body string, width int) (string, error) {
	reg.Lock()
	enabled := reg.cfg.Enabled
	reg.Unlock()

	prepared, refs := extract(body)
	if len(refs) == 0 {
		return r.Render(body)
	}
	if !enabled {
		// Show a readable line instead of glamour's raw link or nothing at
		// all for HTML <img> tags.
		rendered, err := r.Render(prepared)
		if err != nil {
			return rendered, err
		}
		return replaceMarkers(rendered, func(i int) []string {
			return []string{reg.faint.Render("🖼 " + label(refs[i]))}
		}), nil
	}

	rendered, err := r.Render(prepared)
	if err != nil {
		return rendered, err
	}
	return replaceMarkers(rendered, func(i int) []string {
		return block(refs[i], width)
	}), nil
}

func label(ref Ref) string {
	if ref.Alt != "" && !strings.EqualFold(ref.Alt, "image") {
		return ref.Alt
	}
	return "image"
}

// replaceMarkers swaps each marker line with the given lines, keeping the
// marker's indentation.
func replaceMarkers(rendered string, lines func(i int) []string) string {
	out := make([]string, 0, strings.Count(rendered, "\n")+1)
	for _, line := range strings.Split(rendered, "\n") {
		plain := ansi.Strip(line)
		m := markerRe.FindStringSubmatchIndex(plain)
		if m == nil {
			out = append(out, line)
			continue
		}
		var i int
		fmt.Sscanf(plain[m[2]:m[3]], "%d", &i)
		indent := strings.Repeat(" ", ansi.StringWidth(plain[:m[0]]))
		for _, l := range lines(i) {
			out = append(out, indent+l)
		}
	}
	return strings.Join(out, "\n")
}

// block returns the placeholder lines of an image, or a one-line status.
func block(ref Ref, width int) []string {
	reg.Lock()
	defer reg.Unlock()

	e := reg.entries[ref.Url]
	if e == nil {
		e = &entry{url: ref.Url}
		reg.entries[ref.Url] = e
	}
	switch e.state {
	case statePending:
		e.state = stateLoading
		reg.toFetch = append(reg.toFetch, e)
		fallthrough
	case stateLoading:
		return []string{reg.faint.Render("🖼 loading " + label(ref) + "…")}
	case stateFailed:
		return []string{reg.faint.Render(fmt.Sprintf("🖼 %s (%v)", label(ref), e.err))}
	}

	cols, rows := fit(e.pxW, e.pxH, min(reg.cfg.MaxWidth, width), reg.cfg.MaxHeight)
	if cols == 0 || rows == 0 {
		return nil
	}
	if e.id == 0 {
		transmit(e)
	}
	if e.placedCols != cols || e.placedRows != rows {
		// One virtual placement per image: placing it again resizes it.
		fmt.Fprintf(&reg.raw, "\x1b_Ga=p,U=1,i=%d,p=1,c=%d,r=%d,q=2\x1b\\", e.id, cols, rows)
		e.placedCols, e.placedRows = cols, rows
	}

	// The 256-color foreground carries the image id.
	start := fmt.Sprintf("\x1b[38;5;%dm", e.id)
	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		b.WriteString(start)
		for c := 0; c < cols; c++ {
			b.WriteRune(placeholder)
			b.WriteRune(diacritics[r])
			b.WriteRune(diacritics[c])
		}
		b.WriteString("\x1b[39m")
		lines[r] = b.String()
	}
	return lines
}

// fit returns the size in cells of an image of w×h pixels within the limits.
func fit(w, h, maxCols, maxRows int) (int, int) {
	if w <= 0 || h <= 0 || maxCols <= 0 || maxRows <= 0 {
		return 0, 0
	}
	maxCols = min(maxCols, len(diacritics))
	maxRows = min(maxRows, len(diacritics))
	cw, ch := cellSize()
	cols := min(maxCols, max(1, (w+cw-1)/cw))
	rows := max(1, (cols*cw*h/w+ch-1)/ch)
	if rows > maxRows {
		rows = maxRows
		cols = min(maxCols, max(1, (rows*ch*w/h+cw-1)/cw))
	}
	return cols, rows
}

// transmit assigns an id to the image and queues its data for the terminal.
// Ids are recycled: the image previously using the id is deleted.
func transmit(e *entry) {
	id := reg.nextId
	reg.nextId = reg.nextId%255 + 1
	if old := reg.byId[id]; old != nil && old != e {
		old.id, old.placedCols, old.placedRows = 0, 0, 0
		fmt.Fprintf(&reg.raw, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
	}
	reg.byId[id] = e
	e.id = id
	e.placedCols, e.placedRows = 0, 0
	writeTransmit(&reg.raw, id, e.png)
}

// Flush returns the command sending the queued escape sequences to the
// terminal and fetching the requested images. Call it after rendering.
func Flush() tea.Cmd {
	reg.Lock()
	defer reg.Unlock()
	var cmds []tea.Cmd
	if reg.raw.Len() > 0 {
		cmds = append(cmds, tea.Raw(reg.raw.String()))
		reg.raw.Reset()
	}
	for _, e := range reg.toFetch {
		cmds = append(cmds, fetchCmd(e))
	}
	reg.toFetch = nil
	return tea.Batch(cmds...)
}

// Invalidate forgets what the terminal holds, e.g. after the TUI handed
// the terminal over to another program: images are sent again when shown.
func Invalidate() {
	reg.Lock()
	defer reg.Unlock()
	for _, e := range reg.entries {
		e.id, e.placedCols, e.placedRows = 0, 0, 0
	}
	reg.byId = [256]*entry{}
}

// Files returns, for the full screen viewer, the cached file of each image,
// or its URL when it is not downloaded yet.
func Files(refs []Ref) []string {
	reg.Lock()
	defer reg.Unlock()
	files := make([]string, 0, len(refs))
	for _, ref := range refs {
		if e := reg.entries[ref.Url]; e != nil && e.file != "" {
			files = append(files, e.file)
		} else {
			files = append(files, ref.Url)
		}
	}
	return files
}

// LoadedMsg is sent when an image has been downloaded (or failed to).
type LoadedMsg struct {
	Url string
}
