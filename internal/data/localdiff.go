package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// LocalDiffFiles is the diff of a worktree against base (a commit), working
// tree and untracked files included, split by file like the files of a pull
// request.
func LocalDiffFiles(dir, base string) ([]PRFile, error) {
	out, err := git(dir, "diff", "--no-color", "--no-ext-diff", "-M", "-U3", base)
	if err != nil {
		return nil, err
	}
	files := parseGitDiff(out)
	untracked, _ := git(dir, "ls-files", "--others", "--exclude-standard", "-z")
	for _, p := range strings.Split(untracked, "\x00") {
		if p == "" {
			continue
		}
		// --no-index exits with 1 when the files differ, which they do.
		cmd := exec.Command("git", "-C", dir, "diff", "--no-color", "--no-ext-diff", "--no-index", "--", "/dev/null", p)
		b, err := cmd.Output()
		var exit *exec.ExitError
		if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
			continue
		}
		for _, f := range parseGitDiff(string(b)) {
			f.Filename, f.Status = p, "added"
			files = append(files, f)
		}
	}
	return files, nil
}

var diffHeaderRe = regexp.MustCompile(`^diff --git "?a/(.*?)"? "?b/(.*?)"?$`)

func parseGitDiff(out string) []PRFile {
	var files []PRFile
	var cur *PRFile
	var patch []string
	flush := func() {
		if cur != nil {
			cur.Patch = strings.Join(patch, "\n")
			files = append(files, *cur)
		}
		cur, patch = nil, nil
	}
	inHunks := false
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if m := diffHeaderRe.FindStringSubmatch(l); m != nil {
			flush()
			cur = &PRFile{Filename: m[2], Status: "modified"}
			if m[1] != m[2] {
				cur.PreviousFilename = m[1]
			}
			inHunks = false
			continue
		}
		if cur == nil {
			continue
		}
		if !inHunks {
			switch {
			case strings.HasPrefix(l, "new file mode"):
				cur.Status = "added"
			case strings.HasPrefix(l, "deleted file mode"):
				cur.Status = "removed"
			case strings.HasPrefix(l, "rename from "):
				cur.Status = "renamed"
				cur.PreviousFilename = strings.TrimPrefix(l, "rename from ")
			case strings.HasPrefix(l, "rename to "):
				cur.Filename = strings.TrimPrefix(l, "rename to ")
			case strings.HasPrefix(l, "+++ b/"):
				cur.Filename = strings.TrimPrefix(l, "+++ b/")
			case strings.HasPrefix(l, "@@"):
				inHunks = true
			}
			if !inHunks {
				continue
			}
		}
		patch = append(patch, l)
		switch {
		case strings.HasPrefix(l, "+"):
			cur.Additions++
		case strings.HasPrefix(l, "-"):
			cur.Deletions++
		}
	}
	flush()
	return files
}

// LocalFileLines reads a file of the working tree, for the expansion of the
// unchanged lines.
func LocalFileLines(dir, path string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n"), nil
}

// HeadSha is the commit checked out in dir.
func HeadSha(dir string) string {
	out, _ := git(dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out)
}

// IsWorktreeClean reports whether dir has no uncommitted change.
func IsWorktreeClean(dir string) bool {
	out, err := git(dir, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) == ""
}

// LocalNote is a review comment on a worktree. Notes never leave the
// machine: they are written as JSON for gh-dash and as Markdown for people
// and coding agents, one file per branch.
type LocalNote struct {
	DraftReviewComment
	Commit    string `json:",omitempty"` // empty: the working tree
	Subject   string `json:",omitempty"` // of the commit
	Excerpt   string `json:",omitempty"` // diff lines commented
	CreatedAt time.Time
}

// NotesMeta describes the worktree the notes are about.
type NotesMeta struct {
	Repo, Branch, Dir, Base string
}

// NotesDir is where the notes are written: $XDG_DATA_HOME/gh-dash/notes
// (~/.local/share/gh-dash/notes by default).
func NotesDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.TempDir()
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "gh-dash", "notes")
}

var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// NotesFile is the Markdown file of a branch's notes; the JSON file sits
// next to it.
func NotesFile(repo, branch string) string {
	return filepath.Join(NotesDir(), filepath.FromSlash(strings.ToLower(repo)),
		unsafeNameRe.ReplaceAllString(branch, "_")+".md")
}

func notesJSON(repo, branch string) string {
	return strings.TrimSuffix(NotesFile(repo, branch), ".md") + ".json"
}

func LoadLocalNotes(repo, branch string) []LocalNote {
	b, err := os.ReadFile(notesJSON(repo, branch))
	if err != nil {
		return nil
	}
	var notes []LocalNote
	if json.Unmarshal(b, &notes) != nil {
		return nil
	}
	return notes
}

// SaveLocalNotes writes the notes of a branch, or removes its files when
// there is none left.
func SaveLocalNotes(meta NotesMeta, notes []LocalNote) error {
	md, js := NotesFile(meta.Repo, meta.Branch), notesJSON(meta.Repo, meta.Branch)
	if len(notes) == 0 {
		for _, p := range []string{md, js} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(md), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(notes, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(js, b, 0o644); err != nil {
		return err
	}
	return os.WriteFile(md, []byte(RenderNotesMarkdown(meta, notes)), 0o644)
}

// RenderNotesMarkdown lists the notes by file and line, with the code they
// are about, for a coding agent to act on.
func RenderNotesMarkdown(meta NotesMeta, notes []LocalNote) string {
	sorted := append([]LocalNote(nil), notes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Commit != b.Commit {
			return a.Commit < b.Commit
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	var b strings.Builder
	fmt.Fprintf(&b, "# Review notes: %s\n\n", meta.Branch)
	fmt.Fprintf(&b, "- Repository: %s\n- Worktree: `%s`\n- Compared with: %s\n- Notes: %d\n\n",
		meta.Repo, meta.Dir, meta.Base, len(notes))
	b.WriteString("Line numbers refer to the new version of the file (`+` and unchanged lines) " +
		"or to the old one for removed lines (`-`). Notes on a commit refer to the files as of that commit.\n")
	for i, n := range sorted {
		where := fmt.Sprintf("%s:%d", n.Path, n.Line)
		if n.StartLine > 0 && n.StartLine != n.Line {
			where = fmt.Sprintf("%s:%d-%d", n.Path, n.StartLine, n.Line)
		}
		if n.Side == "LEFT" {
			where += " (removed lines)"
		}
		fmt.Fprintf(&b, "\n## %d. `%s`\n\n", i+1, where)
		if n.Commit != "" {
			fmt.Fprintf(&b, "On commit %s", n.Commit)
			if n.Subject != "" {
				fmt.Fprintf(&b, " (%s)", n.Subject)
			}
			b.WriteString("\n\n")
		}
		if n.Excerpt != "" {
			b.WriteString("```diff\n" + strings.TrimRight(n.Excerpt, "\n") + "\n```\n\n")
		}
		b.WriteString(strings.TrimSpace(n.Body) + "\n")
	}
	return b.String()
}

// CommitDiffFiles is the diff of one commit, split by file.
func CommitDiffFiles(dir, sha string) ([]PRFile, error) {
	out, err := git(dir, "show", "--format=", "--no-color", "--no-ext-diff", "-M", "-U3", sha)
	if err != nil {
		return nil, err
	}
	return parseGitDiff(out), nil
}
