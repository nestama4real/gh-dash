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
	"sort"
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
	bold    lipgloss.Style
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
	reg.bold = lipgloss.NewStyle().Bold(true)
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
	markerRe    = regexp.MustCompile(`GHDASH(IMG|GAL)(\d+)X`)
	tableSepRe  = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$`)
)

type imageMatch struct {
	start, end int
	ref        Ref
}

// scan finds the images of a text, in document order.
func scan(text string) []imageMatch {
	var matches []imageMatch
	for _, m := range mdImageRe.FindAllStringSubmatchIndex(text, -1) {
		matches = append(matches, imageMatch{m[0], m[1], Ref{
			Alt: text[m[2]:m[3]], Url: text[m[4]:m[5]],
		}})
	}
	for _, m := range htmlImageRe.FindAllStringIndex(text, -1) {
		tag := text[m[0]:m[1]]
		src := srcAttrRe.FindStringSubmatch(tag)
		if src == nil {
			continue
		}
		ref := Ref{Url: src[1]}
		if alt := altAttrRe.FindStringSubmatch(tag); alt != nil {
			ref.Alt = alt[1]
		}
		matches = append(matches, imageMatch{m[0], m[1], ref})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].start < matches[j].start })
	// Drop overlapping matches, e.g. an <img> inside a markdown image.
	kept := matches[:0]
	last := 0
	for _, m := range matches {
		if m.start >= last {
			kept = append(kept, m)
			last = m.end
		}
	}
	return kept
}

// FindImages lists the images of a markdown body, in order.
func FindImages(body string) []Ref {
	matches := scan(body)
	refs := make([]Ref, 0, len(matches))
	for _, m := range matches {
		refs = append(refs, m.ref)
	}
	return refs
}

// galleryCell is a cell of a markdown table holding images: GitHub users
// put screenshots side by side this way.
type galleryCell struct {
	refs []Ref
	text string
}

type gallery struct {
	headers []string
	rows    [][]galleryCell
}

func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	cells := strings.Split(line, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// extractGalleries replaces the markdown tables holding images with markers.
func extractGalleries(body string) (string, []gallery) {
	lines := strings.Split(body, "\n")
	var out []string
	var galleries []gallery
	for i := 0; i < len(lines); i++ {
		isRow := func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "|") }
		if !isRow(lines[i]) || i+1 >= len(lines) || !tableSepRe.MatchString(lines[i+1]) {
			out = append(out, lines[i])
			continue
		}
		end := i + 2
		for end < len(lines) && isRow(lines[end]) {
			end++
		}
		table := strings.Join(lines[i:end], "\n")
		if len(scan(table)) == 0 {
			out = append(out, lines[i:end]...)
			i = end - 1
			continue
		}
		g := gallery{headers: splitTableRow(lines[i])}
		for _, l := range lines[i+2 : end] {
			var row []galleryCell
			for _, c := range splitTableRow(l) {
				cell := galleryCell{}
				for _, m := range scan(c) {
					cell.refs = append(cell.refs, m.ref)
				}
				if len(cell.refs) == 0 {
					cell.text = c
				}
				row = append(row, cell)
			}
			g.rows = append(g.rows, row)
		}
		out = append(out, "", fmt.Sprintf("GHDASHGAL%dX", len(galleries)), "")
		galleries = append(galleries, g)
		i = end - 1
	}
	return strings.Join(out, "\n"), galleries
}

// extract replaces image tables with gallery markers and the other images
// with image markers, each in its own paragraph. clean is applied to the
// rest of the body, once the image tables are out of the way.
func extract(body string, clean ...func(string) string) (string, []Ref, []gallery) {
	body, galleries := extractGalleries(body)
	for _, c := range clean {
		body = c(body)
	}
	matches := scan(body)
	if len(matches) == 0 {
		return body, nil, galleries
	}
	var refs []Ref
	var b strings.Builder
	last := 0
	for _, m := range matches {
		b.WriteString(body[last:m.start])
		fmt.Fprintf(&b, "\n\nGHDASHIMG%dX\n\n", len(refs))
		refs = append(refs, m.ref)
		last = m.end
	}
	b.WriteString(body[last:])
	return b.String(), refs, galleries
}

// Renderer is the subset of glamour's renderer used here.
type Renderer interface {
	Render(string) (string, error)
}

// RenderMarkdown renders a markdown body with its images inline, fitting in
// width cells. Images that are not loaded yet are requested and show a
// one-line placeholder until they are.
//
// clean functions (e.g. gh-dash's removal of markdown tables, which the
// renderer handles poorly) run after the tables holding images have been
// turned into galleries, so that those survive.
func RenderMarkdown(r Renderer, body string, width int, clean ...func(string) string) (string, error) {
	reg.Lock()
	enabled := reg.cfg.Enabled
	reg.Unlock()

	prepared, refs, galleries := extract(body, clean...)
	if len(refs) == 0 && len(galleries) == 0 {
		return r.Render(prepared)
	}
	rendered, err := r.Render(prepared)
	if err != nil {
		return rendered, err
	}
	image := func(ref Ref, w int) []string {
		if !enabled {
			// A readable line instead of glamour's raw link, or nothing at
			// all for HTML <img> tags.
			return []string{reg.faint.Render(ansi.Truncate("🖼 "+label(ref), w, "…"))}
		}
		return block(ref, w)
	}
	return replaceMarkers(rendered, func(kind string, i int, avail int) []string {
		if kind == "GAL" {
			return renderGallery(galleries[i], avail, image)
		}
		return image(refs[i], avail)
	}, width), nil
}

const galleryGap = 2

// renderGallery lays the images of a table out side by side, under the
// table's headers.
func renderGallery(g gallery, width int, image func(Ref, int) []string) []string {
	ncols := len(g.headers)
	for _, row := range g.rows {
		ncols = max(ncols, len(row))
	}
	if ncols == 0 {
		return nil
	}
	colW := max(4, (width-galleryGap*(ncols-1))/ncols)
	pad := func(s string) string {
		s = ansi.Truncate(s, colW, "…")
		return s + strings.Repeat(" ", max(0, colW-ansi.StringWidth(s)))
	}
	join := func(cols [][]string) []string {
		h := 0
		for _, c := range cols {
			h = max(h, len(c))
		}
		lines := make([]string, h)
		for l := 0; l < h; l++ {
			parts := make([]string, len(cols))
			for c, col := range cols {
				if l < len(col) {
					parts[c] = pad(col[l])
				} else {
					parts[c] = strings.Repeat(" ", colW)
				}
			}
			lines[l] = strings.TrimRight(strings.Join(parts, strings.Repeat(" ", galleryGap)), " ")
		}
		return lines
	}

	var out []string
	hasHeader := false
	headers := make([][]string, ncols)
	for c := 0; c < ncols; c++ {
		if c < len(g.headers) && g.headers[c] != "" {
			headers[c] = []string{reg.bold.Render(g.headers[c])}
			hasHeader = true
		}
	}
	if hasHeader {
		out = append(out, join(headers)...)
	}
	for r, row := range g.rows {
		if r > 0 {
			out = append(out, "")
		}
		cols := make([][]string, ncols)
		for c := 0; c < ncols && c < len(row); c++ {
			cell := row[c]
			if len(cell.refs) == 0 {
				cols[c] = []string{cell.text}
				continue
			}
			for i, ref := range cell.refs {
				if i > 0 {
					cols[c] = append(cols[c], "")
				}
				cols[c] = append(cols[c], image(ref, colW)...)
			}
		}
		out = append(out, join(cols)...)
	}
	return out
}

func label(ref Ref) string {
	if ref.Alt != "" && !strings.EqualFold(ref.Alt, "image") {
		return ref.Alt
	}
	return "image"
}

// replaceMarkers swaps each marker line with the given lines, keeping the
// marker's indentation.
func replaceMarkers(rendered string, lines func(kind string, i, width int) []string, width int) string {
	out := make([]string, 0, strings.Count(rendered, "\n")+1)
	for _, line := range strings.Split(rendered, "\n") {
		plain := ansi.Strip(line)
		m := markerRe.FindStringSubmatchIndex(plain)
		if m == nil {
			out = append(out, line)
			continue
		}
		var i int
		fmt.Sscanf(plain[m[4]:m[5]], "%d", &i)
		indentW := ansi.StringWidth(plain[:m[0]])
		indent := strings.Repeat(" ", indentW)
		for _, l := range lines(plain[m[2]:m[3]], i, max(1, width-indentW)) {
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
		return []string{reg.faint.Render(ansi.Truncate("🖼 loading "+label(ref)+"…", width, "…"))}
	case stateFailed:
		return []string{reg.faint.Render(ansi.Truncate(fmt.Sprintf("🖼 %s (%v)", label(ref), e.err), width, "…"))}
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
