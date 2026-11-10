package tui

import (
	"os/exec"

	tea "charm.land/bubbletea/v2"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/components/prrow"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/images"
)

type imagesViewerClosedMsg struct{}

// viewerScript shows images full screen with kitty's icat, one at a time.
const viewerScript = `
n=$#; i=1
trap 'printf "\033[2J\033[H"' EXIT
while :; do
  eval "f=\${$i}"
  printf '\033[2J\033[H'
  kitten icat --align center --stdin no "$f" 2>/dev/null ||
    printf '\n  Could not display %s\n' "$f"
  printf '\n  \033[2m%d/%d   ←/→ h/l previous/next   q/esc back\033[0m' "$i" "$n"
  IFS= read -rsn1 k
  if [ "$k" = $'\e' ]; then
    IFS= read -rsn2 -t 0.05 rest
    case "$rest" in
      '[C') k=l ;;
      '[D') k=h ;;
      *) k=q ;;
    esac
  fi
  case "$k" in
    q) break ;;
    l|n|' '|'') [ "$i" -lt "$n" ] && i=$((i+1)) ;;
    h|p) [ "$i" -gt 1 ] && i=$((i-1)) ;;
  esac
done
`

// viewImages opens the images of the selected issue or PR full screen.
func (m *Model) viewImages(row data.RowData) tea.Cmd {
	var bodies []string
	switch r := row.(type) {
	case *data.IssueData:
		if r != nil {
			bodies = append(bodies, r.Body)
			for _, c := range r.Comments.Nodes {
				bodies = append(bodies, c.Body)
			}
		}
	case *prrow.Data:
		if r != nil && r.IsEnriched {
			bodies = append(bodies, r.Enriched.Body)
			for _, c := range r.Enriched.Comments.Nodes {
				bodies = append(bodies, c.Body)
			}
		}
	}
	var refs []images.Ref
	for _, b := range bodies {
		refs = append(refs, images.FindImages(b)...)
	}
	if len(refs) == 0 {
		return m.notifyErr("No image in this description")
	}
	if _, err := exec.LookPath("kitten"); err != nil {
		return m.notifyErr("Viewing images needs kitty's kitten command")
	}
	args := append([]string{"-c", viewerScript, "gh-dash-images"}, images.Files(refs)...)
	c := exec.Command("bash", args...)
	return tea.ExecProcess(c, func(error) tea.Msg { return imagesViewerClosedMsg{} })
}
