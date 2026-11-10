package images

import (
	"strings"
	"testing"
)

func TestFindImages(t *testing.T) {
	body := "Intro ![screen](https://example.com/a.png) text\n\n" +
		`<img width="300" alt="Phone" src="https://github.com/user-attachments/assets/b">` +
		"\n\n![](<https://example.com/c.jpg> \"title\")"
	refs := FindImages(body)
	want := []Ref{
		{Alt: "screen", Url: "https://example.com/a.png"},
		{Alt: "Phone", Url: "https://github.com/user-attachments/assets/b"},
		{Alt: "", Url: "https://example.com/c.jpg"},
	}
	if len(refs) != len(want) {
		t.Fatalf("got %d refs: %+v", len(refs), refs)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Errorf("ref %d: got %+v, want %+v", i, refs[i], want[i])
		}
	}
}

func TestReplaceMarkers(t *testing.T) {
	prepared, refs := extract("a ![x](u) b")
	if len(refs) != 1 || !strings.Contains(prepared, "GHDASHIMG0X") {
		t.Fatalf("bad extraction: %q %+v", prepared, refs)
	}
	rendered := "  a\n\n  \x1b[1mGHDASHIMG0X\x1b[0m\n\n  b"
	out := replaceMarkers(rendered, func(int) []string { return []string{"L1", "L2"} })
	if out != "  a\n\n  L1\n  L2\n\n  b" {
		t.Errorf("got %q", out)
	}
}

func TestFit(t *testing.T) {
	cw, ch := cellSize()
	// A wide image is limited by the width.
	cols, rows := fit(cw*200, ch*10, 80, 20)
	if cols != 80 || rows > 20 || rows < 1 {
		t.Errorf("wide: %dx%d", cols, rows)
	}
	// A tall image is limited by the height.
	cols, rows = fit(cw*10, ch*200, 80, 20)
	if rows != 20 || cols > 80 || cols < 1 {
		t.Errorf("tall: %dx%d", cols, rows)
	}
	// A small image keeps its size.
	cols, rows = fit(cw*5, ch*3, 80, 20)
	if cols != 5 || rows != 3 {
		t.Errorf("small: %dx%d", cols, rows)
	}
}
