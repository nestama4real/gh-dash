package images

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// writeTransmit queues the PNG data of an image under id, in chunks as the
// protocol requires. q=2 silences the terminal's replies.
func writeTransmit(b *strings.Builder, id int, png []byte) {
	data := base64.StdEncoding.EncodeToString(png)
	const chunk = 4096
	for i := 0; i < len(data); i += chunk {
		end := min(i+chunk, len(data))
		more := 0
		if end < len(data) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(b, "\x1b_Ga=t,f=100,t=d,i=%d,q=2,m=%d;%s\x1b\\", id, more, data[i:end])
		} else {
			fmt.Fprintf(b, "\x1b_Gm=%d;%s\x1b\\", more, data[i:end])
		}
	}
}

// cellSize returns the size of a terminal cell in pixels.
func cellSize() (int, int) {
	for _, f := range []*os.File{os.Stdout, os.Stdin, os.Stderr} {
		ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
		if err == nil && ws.Col > 0 && ws.Row > 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
			return max(1, int(ws.Xpixel)/int(ws.Col)), max(1, int(ws.Ypixel)/int(ws.Row))
		}
	}
	return 10, 20
}
