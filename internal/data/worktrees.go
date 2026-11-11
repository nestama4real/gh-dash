package data

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gh "github.com/cli/go-gh/v2/pkg/api"
)

// Worktree is a git worktree of the "Current Work" view, compared with the
// base branch like a pull request: committed and uncommitted changes since
// the merge base.
type Worktree struct {
	Repo      string // owner/name on GitHub
	Path      string
	Branch    string // empty when detached
	Head      string
	Base      string // e.g. origin/main
	MergeBase string

	Ahead, Behind        int
	Additions, Deletions int
	Files                []WorktreeFile
	Dirty                bool // uncommitted changes, untracked files included
	Commits              []WorktreeCommit
	ChangedAt            time.Time

	PR    *BranchPR
	Issue int // linked issue, 0 when none
	Err   error
}

type WorktreeFile struct {
	Path                 string
	Additions, Deletions int
	Binary               bool
	Untracked            bool
}

type WorktreeCommit struct {
	Sha, Subject string
	At           time.Time
}

// BranchPR is the latest pull request opened from a branch.
type BranchPR struct {
	Number  int
	Title   string
	Url     string
	State   string // OPEN, CLOSED, MERGED
	IsDraft bool
	Issues  []int // issues it closes
}

func (w *Worktree) GetRepoNameWithOwner() string { return w.Repo }
func (w *Worktree) GetTitle() string {
	if w.Branch == "" {
		return "(detached " + shortSha(w.Head) + ")"
	}
	return w.Branch
}

func (w *Worktree) GetNumber() int {
	if w.PR != nil {
		return w.PR.Number
	}
	return 0
}

// GetUrl is the PR, else the issue, else the branch on GitHub.
func (w *Worktree) GetUrl() string {
	switch {
	case w.PR != nil:
		return w.PR.Url
	case w.Issue > 0:
		return fmt.Sprintf("https://github.com/%s/issues/%d", w.Repo, w.Issue)
	case w.Branch != "":
		return fmt.Sprintf("https://github.com/%s/tree/%s", w.Repo, w.Branch)
	}
	return "https://github.com/" + w.Repo
}

func (w *Worktree) GetUpdatedAt() time.Time { return w.ChangedAt }

func shortSha(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}

var remoteRe = regexp.MustCompile(`github\.com[:/]([^/]+/[^/]+?)(?:\.git)?/?$`)

// RepoNameFromRemote returns owner/name of the origin remote.
func RepoNameFromRemote(dir string) string {
	out, err := git(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	if m := remoteRe.FindStringSubmatch(strings.TrimSpace(out)); m != nil {
		return m[1]
	}
	return ""
}

// DefaultBase is origin's default branch, e.g. origin/main.
func DefaultBase(dir string) string {
	out, err := git(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	for _, b := range []string{"origin/main", "origin/master"} {
		if _, err := git(dir, "rev-parse", "--verify", "--quiet", b); err == nil {
			return b
		}
	}
	return "origin/main"
}

// ListWorktrees lists the worktrees of the clone in repoDir with their
// changes against base (origin's default branch when empty).
func ListWorktrees(repoDir, base string) ([]Worktree, error) {
	out, err := git(repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	repo := RepoNameFromRemote(repoDir)
	if base == "" {
		base = DefaultBase(repoDir)
	}
	var wts []Worktree
	var cur *Worktree
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			wts = append(wts, Worktree{Repo: repo, Base: base, Path: strings.TrimPrefix(line, "worktree ")})
			cur = &wts[len(wts)-1]
		case cur == nil:
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "bare", strings.HasPrefix(line, "prunable"):
			cur.Path = "" // skipped below
		}
	}
	res := wts[:0]
	for _, w := range wts {
		if w.Path != "" {
			res = append(res, w)
		}
	}
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func(w *Worktree) {
			defer wg.Done()
			w.Err = w.load()
		}(&res[i])
	}
	wg.Wait()
	return res, nil
}

func (w *Worktree) load() error {
	mb, err := git(w.Path, "merge-base", w.Base, "HEAD")
	if err != nil {
		return fmt.Errorf("merge-base with %s: %w", w.Base, err)
	}
	w.MergeBase = strings.TrimSpace(mb)

	if out, err := git(w.Path, "rev-list", "--left-right", "--count", w.Base+"...HEAD"); err == nil {
		if f := strings.Fields(out); len(f) == 2 {
			w.Behind, _ = strconv.Atoi(f[0])
			w.Ahead, _ = strconv.Atoi(f[1])
		}
	}

	files, err := w.changedFiles()
	if err != nil {
		return err
	}
	w.Files = files
	for _, f := range files {
		w.Additions += f.Additions
		w.Deletions += f.Deletions
	}

	status, _ := git(w.Path, "status", "--porcelain")
	w.Dirty = strings.TrimSpace(status) != ""

	if out, err := git(w.Path, "log", "--format=%h%x00%ct%x00%s", w.MergeBase+"..HEAD"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			p := strings.SplitN(l, "\x00", 3)
			if len(p) != 3 {
				continue
			}
			ts, _ := strconv.ParseInt(p[1], 10, 64)
			w.Commits = append(w.Commits, WorktreeCommit{Sha: p[0], At: time.Unix(ts, 0), Subject: p[2]})
		}
	}
	if out, err := git(w.Path, "log", "-1", "--format=%ct"); err == nil {
		if ts, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil {
			w.ChangedAt = time.Unix(ts, 0)
		}
	}
	// Uncommitted edits are newer than the last commit.
	for _, f := range w.Files {
		if st, err := os.Stat(filepath.Join(w.Path, f.Path)); err == nil && st.ModTime().After(w.ChangedAt) && w.Dirty {
			w.ChangedAt = st.ModTime()
		}
	}
	if n := issueFromBranch(w.Branch); n > 0 {
		w.Issue = n
	}
	return nil
}

// changedFiles lists the files changed since the merge base, working tree
// and untracked files included.
func (w *Worktree) changedFiles() ([]WorktreeFile, error) {
	out, err := git(w.Path, "diff", "--numstat", "-z", w.MergeBase)
	if err != nil {
		return nil, err
	}
	var files []WorktreeFile
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if p == "" {
			continue
		}
		f := strings.SplitN(p, "\t", 3)
		if len(f) != 3 {
			continue
		}
		path := f[2]
		if path == "" && i+2 < len(parts) { // rename: old\0new\0
			path = parts[i+2]
			i += 2
		}
		wf := WorktreeFile{Path: path}
		if f[0] == "-" {
			wf.Binary = true
		} else {
			wf.Additions, _ = strconv.Atoi(f[0])
			wf.Deletions, _ = strconv.Atoi(f[1])
		}
		files = append(files, wf)
	}
	untracked, _ := git(w.Path, "ls-files", "--others", "--exclude-standard", "-z")
	for _, p := range strings.Split(untracked, "\x00") {
		if p == "" {
			continue
		}
		wf := WorktreeFile{Path: p, Untracked: true}
		if b, err := os.ReadFile(filepath.Join(w.Path, p)); err == nil {
			if bytes.IndexByte(b, 0) >= 0 {
				wf.Binary = true
			} else if len(b) > 0 {
				wf.Additions = bytes.Count(b, []byte("\n"))
				if b[len(b)-1] != '\n' {
					wf.Additions++
				}
			}
		}
		files = append(files, wf)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// issueFromBranch reads the issue number of branches like fix/409-foo,
// 409-foo or feature/issue-226.
var branchIssueRe = regexp.MustCompile(`(?:^|[/_-])(?:issue-|gh-|#)?(\d{1,6})(?:[-_/]|$)`)

func issueFromBranch(branch string) int {
	m := branchIssueRe.FindStringSubmatch(branch)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

var (
	branchPRsMu    sync.Mutex
	branchPRsCache = map[string]struct {
		at  time.Time
		prs map[string]BranchPR
	}{}
)

// FetchBranchPRs maps head branch names to their latest pull request, for
// the 100 most recently updated PRs of the repository. Cached 30 seconds.
func FetchBranchPRs(repo string) (map[string]BranchPR, error) {
	branchPRsMu.Lock()
	if c, ok := branchPRsCache[repo]; ok && time.Since(c.at) < 30*time.Second {
		branchPRsMu.Unlock()
		return c.prs, nil
	}
	branchPRsMu.Unlock()

	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, fmt.Errorf("bad repository %q", repo)
	}
	client, err := gh.DefaultGraphQLClient()
	if err != nil {
		return nil, err
	}
	var resp struct {
		Repository struct {
			PullRequests struct {
				Nodes []struct {
					Number                  int
					Title                   string
					Url                     string
					State                   string
					IsDraft                 bool
					HeadRefName             string
					ClosingIssuesReferences struct {
						Nodes []struct{ Number int }
					}
				}
			}
		}
	}
	err = client.Do(`query BranchPRs($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    pullRequests(first: 100, orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {
        number title url state isDraft headRefName
        closingIssuesReferences(first: 5) { nodes { number } }
      }
    }
  }
}`, map[string]any{"owner": owner, "name": name}, &resp)
	if err != nil {
		return nil, err
	}
	prs := map[string]BranchPR{}
	for _, n := range resp.Repository.PullRequests.Nodes {
		if _, seen := prs[n.HeadRefName]; seen {
			continue // keep the most recently updated one
		}
		pr := BranchPR{Number: n.Number, Title: n.Title, Url: n.Url, State: n.State, IsDraft: n.IsDraft}
		for _, i := range n.ClosingIssuesReferences.Nodes {
			pr.Issues = append(pr.Issues, i.Number)
		}
		prs[n.HeadRefName] = pr
	}
	branchPRsMu.Lock()
	branchPRsCache[repo] = struct {
		at  time.Time
		prs map[string]BranchPR
	}{time.Now(), prs}
	branchPRsMu.Unlock()
	return prs, nil
}

// FetchWorktrees lists the worktrees and links them to their PR and issue.
// A failure to reach GitHub only drops the links.
func FetchWorktrees(repoDir, base string) ([]Worktree, error) {
	wts, err := ListWorktrees(repoDir, base)
	if err != nil || len(wts) == 0 {
		return wts, err
	}
	prs, prErr := FetchBranchPRs(wts[0].Repo)
	for i := range wts {
		w := &wts[i]
		pr, ok := prs[w.Branch]
		if !ok || w.Branch == "" {
			continue
		}
		w.PR = &pr
		if len(pr.Issues) > 0 && !containsInt(pr.Issues, w.Issue) {
			w.Issue = pr.Issues[0]
		}
	}
	sort.SliceStable(wts, func(i, j int) bool { return wts[i].ChangedAt.After(wts[j].ChangedAt) })
	return wts, prErr
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
