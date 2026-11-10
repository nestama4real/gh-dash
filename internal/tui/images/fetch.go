package images

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"
	"github.com/cli/go-gh/v2/pkg/auth"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxImageBytes = 25 << 20

var httpClient = &http.Client{Timeout: 30 * time.Second}

func fetchCmd(e *entry) tea.Cmd {
	url := e.url
	return func() tea.Msg {
		raw, file, err := download(url)
		var pngData []byte
		var w, h int
		if err == nil {
			pngData, w, h, err = prepare(raw)
		}
		reg.Lock()
		if err != nil {
			log.Warn("Failed loading image", "url", url, "err", err)
			e.state, e.err = stateFailed, shortError(err)
		} else {
			e.state, e.png, e.pxW, e.pxH, e.file = stateReady, pngData, w, h, file
		}
		reg.Unlock()
		return LoadedMsg{Url: url}
	}
}

func shortError(err error) error {
	var de *decodeError
	if errors.As(err, &de) {
		return errors.New("unsupported format")
	}
	return errors.New("not available")
}

type decodeError struct{ err error }

func (e *decodeError) Error() string { return e.err.Error() }

func cacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "gh-dash", "images")
}

// download returns the image bytes, from the disk cache when possible.
func download(url string) ([]byte, string, error) {
	sum := sha1.Sum([]byte(url))
	file := filepath.Join(cacheDir(), hex.EncodeToString(sum[:]))
	if b, err := os.ReadFile(file); err == nil {
		return b, file, nil
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	// Attachments of private repositories need the GitHub token. Go drops
	// the header when redirected to another host (the storage).
	if isGitHubHost(req.URL.Host) {
		if token, _ := auth.TokenForHost("github.com"); token != "" {
			req.Header.Set("Authorization", "token "+token)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > maxImageBytes {
		return nil, "", errors.New("image too large")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err == nil {
		if os.WriteFile(file+".tmp", b, 0o600) == nil {
			_ = os.Rename(file+".tmp", file)
		}
	}
	return b, file, nil
}

func isGitHubHost(host string) bool {
	return host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

// prepare decodes an image and scales it down to what the largest allowed
// placement can show, so that little data is sent to the terminal.
func prepare(raw []byte) ([]byte, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, 0, 0, &decodeError{err}
	}
	b := img.Bounds()
	reg.Lock()
	maxCols, maxRows := reg.cfg.MaxWidth, reg.cfg.MaxHeight
	reg.Unlock()
	cw, ch := cellSize()
	maxW, maxH := maxCols*cw, maxRows*ch
	w, h := b.Dx(), b.Dy()
	if w > maxW || h > maxH {
		scale := min(float64(maxW)/float64(w), float64(maxH)/float64(h))
		nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
		dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, 0, 0, err
	}
	// Keep the original size: the placement is computed from it.
	return buf.Bytes(), w, h, nil
}
