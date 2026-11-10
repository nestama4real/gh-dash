package data

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	gh "github.com/cli/go-gh/v2/pkg/api"
)

// PRFile is a file changed by a pull request, with its unified diff.
type PRFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"` // added, removed, modified, renamed...
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"` // empty for binary or too large files
}

// FetchPRFiles lists the files of a pull request with their patches.
func FetchPRFiles(repoNameWithOwner string, number int) ([]PRFile, error) {
	client, err := gh.DefaultRESTClient()
	if err != nil {
		return nil, err
	}
	var files []PRFile
	for page := 1; page <= 30; page++ {
		var batch []PRFile
		path := fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100&page=%d",
			repoNameWithOwner, number, page)
		if err := client.Get(path, &batch); err != nil {
			return nil, err
		}
		files = append(files, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return files, nil
}

// PRReviewThread is an existing review conversation on a line of the diff.
type PRReviewThread struct {
	Path       string
	Line       int
	StartLine  int
	DiffSide   string // LEFT or RIGHT
	IsOutdated bool
	IsResolved bool
	Comments   []PRReviewThreadComment
}

type PRReviewThreadComment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

// PRReviewContext is what a review needs besides the diff.
type PRReviewContext struct {
	Id      string // node id, to submit the review
	HeadSha string
	Threads []PRReviewThread
}

func FetchPRReviewContext(repoNameWithOwner string, number int) (PRReviewContext, error) {
	client, err := gh.DefaultGraphQLClient()
	if err != nil {
		return PRReviewContext{}, err
	}
	owner, name, ok := strings.Cut(repoNameWithOwner, "/")
	if !ok {
		return PRReviewContext{}, fmt.Errorf("bad repository %q", repoNameWithOwner)
	}
	var resp struct {
		Repository struct {
			PullRequest struct {
				Id            string
				HeadRefOid    string
				ReviewThreads struct {
					Nodes []struct {
						Path       string
						Line       int
						StartLine  int
						DiffSide   string
						IsOutdated bool
						IsResolved bool
						Comments   struct {
							Nodes []struct {
								Author    struct{ Login string }
								Body      string
								CreatedAt time.Time
							}
						}
					}
				}
			}
		}
	}
	err = client.Do(`query ReviewContext($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      id headRefOid
      reviewThreads(first: 100) {
        nodes {
          path line startLine diffSide isOutdated isResolved
          comments(first: 50) { nodes { author { login } body createdAt } }
        }
      }
    }
  }
}`, map[string]any{"owner": owner, "name": name, "number": number}, &resp)
	if err != nil {
		return PRReviewContext{}, err
	}
	pr := resp.Repository.PullRequest
	rc := PRReviewContext{Id: pr.Id, HeadSha: pr.HeadRefOid}
	for _, t := range pr.ReviewThreads.Nodes {
		thread := PRReviewThread{
			Path: t.Path, Line: t.Line, StartLine: t.StartLine, DiffSide: t.DiffSide,
			IsOutdated: t.IsOutdated, IsResolved: t.IsResolved,
		}
		for _, c := range t.Comments.Nodes {
			thread.Comments = append(thread.Comments, PRReviewThreadComment{
				Author: c.Author.Login, Body: c.Body, CreatedAt: c.CreatedAt,
			})
		}
		rc.Threads = append(rc.Threads, thread)
	}
	return rc, nil
}

// FetchFileLines returns the lines of a file at a commit.
func FetchFileLines(repoNameWithOwner, path, ref string) ([]string, error) {
	client, err := gh.DefaultRESTClient()
	if err != nil {
		return nil, err
	}
	var resp struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	escaped := strings.ReplaceAll(url.PathEscape(path), "%2F", "/")
	if err := client.Get(fmt.Sprintf("repos/%s/contents/%s?ref=%s",
		repoNameWithOwner, escaped, url.QueryEscape(ref)), &resp); err != nil {
		return nil, err
	}
	if resp.Encoding != "base64" {
		return nil, fmt.Errorf("the file is too large to be shown")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(resp.Content, "\n", ""))
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	return strings.Split(text, "\n"), nil
}

// DraftReviewComment is a comment on a line (or a range of lines) of the
// diff, sent with the review.
type DraftReviewComment struct {
	Path      string
	Line      int
	Side      string // LEFT (deleted lines) or RIGHT
	StartLine int    // 0 for a single line
	StartSide string
	Body      string
}

// SubmitPRReview submits a review in one go: its verdict (APPROVE,
// REQUEST_CHANGES or COMMENT), its body and its line comments.
func SubmitPRReview(prId, event, body string, comments []DraftReviewComment) error {
	client, err := gh.DefaultGraphQLClient()
	if err != nil {
		return err
	}
	threads := make([]map[string]any, 0, len(comments))
	for _, c := range comments {
		t := map[string]any{"path": c.Path, "line": c.Line, "side": c.Side, "body": c.Body}
		if c.StartLine > 0 && c.StartLine != c.Line {
			t["startLine"] = c.StartLine
			t["startSide"] = c.StartSide
		}
		threads = append(threads, t)
	}
	input := map[string]any{"pullRequestId": prId, "event": event, "threads": threads}
	if strings.TrimSpace(body) != "" {
		input["body"] = body
	}
	var resp struct{}
	return client.Do(`mutation SubmitReview($input: AddPullRequestReviewInput!) {
  addPullRequestReview(input: $input) { pullRequestReview { id } }
}`, map[string]any{"input": input}, &resp)
}
