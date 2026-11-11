package data

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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

// Local review notes are kept on disk, per worktree branch.
func notesPath(key string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	name := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(key, "_")
	return filepath.Join(dir, "gh-dash", "notes", name+".json")
}

func LoadLocalNotes(key string) []DraftReviewComment {
	b, err := os.ReadFile(notesPath(key))
	if err != nil {
		return nil
	}
	var notes []DraftReviewComment
	if json.Unmarshal(b, &notes) != nil {
		return nil
	}
	return notes
}

func SaveLocalNotes(key string, notes []DraftReviewComment) error {
	p := notesPath(key)
	if len(notes) == 0 {
		err := os.Remove(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}
