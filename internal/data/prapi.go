package data

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"charm.land/log/v2"
	gh "github.com/cli/go-gh/v2/pkg/api"
	graphql "github.com/cli/shurcooL-graphql"
	checks "github.com/dlvhdr/x/gh-checks"
	"github.com/shurcooL/githubv4"

	"github.com/dlvhdr/gh-dash/v4/internal/config"
	"github.com/dlvhdr/gh-dash/v4/internal/tui/theme"
)

type SuggestedReviewer struct {
	IsAuthor    bool
	IsCommenter bool
	Reviewer    struct {
		Login string
	}
}

type EnrichedPullRequestData struct {
	Url     string
	Number  int
	Title   string
	Body    string
	State   string
	IsDraft bool
	Author  struct {
		Login string
	}
	AuthorAssociation string
	UpdatedAt         time.Time
	CreatedAt         time.Time
	Mergeable         string
	ReviewDecision    string
	Additions         int
	Deletions         int
	HeadRefName       string
	BaseRefName       string
	HeadRepository    struct {
		Name string
	}
	HeadRef struct {
		Name string
	}
	Labels             PRLabels  `graphql:"labels(first: 6)"`
	Assignees          Assignees `graphql:"assignees(first: 3)"`
	Repository         Repository
	Commits            LastCommitWithStatusChecks `graphql:"commits(last: 1)"`
	AllCommits         AllCommits                 `graphql:"allCommits: commits(last: 100)"`
	Comments           CommentsWithBody           `graphql:"comments(last: 50, orderBy: { field: UPDATED_AT, direction: DESC })"`
	ReviewThreads      ReviewThreadsWithComments  `graphql:"reviewThreads(last: 50)"`
	ReviewRequests     ReviewRequests             `graphql:"reviewRequests(last: 100)"`
	Reviews            Reviews                    `graphql:"reviews(last: 100)"`
	SuggestedReviewers []SuggestedReviewer
	Files              ChangedFiles `graphql:"files(first: 20)"`
}

type PullRequestData struct {
	Number int
	Title  string
	Author struct {
		Login string
	}
	AuthorAssociation string
	UpdatedAt         time.Time
	CreatedAt         time.Time
	Url               string
	State             string
	Mergeable         string
	ReviewDecision    string
	Additions         int
	Deletions         int
	HeadRefName       string
	BaseRefName       string
	HeadRepository    struct {
		Name string
	}
	HeadRef struct {
		Name string
	}
	Repository       Repository
	Assignees        Assignees            `graphql:"assignees(first: 3)"`
	Comments         Comments             `graphql:"comments"`
	ReviewThreads    ReviewThreads        `graphql:"reviewThreads"`
	Reviews          ReviewsNumber        `graphql:"reviews"`
	ReviewRequests   ReviewRequestsNumber `graphql:"reviewRequests"`
	IsDraft          bool
	IsInMergeQueue   bool
	Commits          LastCommitStatus `graphql:"commits(last: 1)"`
	Labels           PRLabels         `graphql:"labels(first: 6)"`
	MergeStateStatus MergeStateStatus `graphql:"mergeStateStatus"`
}

type LastCommitStatus struct {
	Nodes []struct {
		Commit struct {
			StatusCheckRollup struct {
				State graphql.String
			}
		}
	}
}

type CheckRun struct {
	Name       graphql.String
	Status     graphql.String
	Conclusion checks.CheckRunState
	CheckSuite struct {
		Creator struct {
			Login graphql.String
		}
		WorkflowRun struct {
			Workflow struct {
				Name graphql.String
			}
		}
	}
}

type StatusContext struct {
	Context graphql.String
	State   graphql.String
	Creator struct {
		Login graphql.String
	}
}

type CheckSuiteNode struct {
	Status     graphql.String
	Conclusion graphql.String

	App struct {
		Name graphql.String
	}

	WorkflowRun struct {
		Workflow struct {
			Name graphql.String
		}
	}
}

type CheckSuites struct {
	TotalCount graphql.Int
	Nodes      []CheckSuiteNode
}

type StatusCheckRollupStats struct {
	State    checks.CommitState
	Contexts struct {
		TotalCount                 graphql.Int
		CheckRunCount              graphql.Int
		CheckRunCountsByState      []ContextCountByState
		StatusContextCount         graphql.Int
		StatusContextCountsByState []ContextCountByState
	} `graphql:"contexts(last: 1)"`
}

type AllCommits struct {
	Nodes []struct {
		Commit struct {
			AbbreviatedOid  string
			CommittedDate   time.Time
			MessageHeadline string
			Author          struct {
				Name string
				User struct {
					Login string
				}
			}
			StatusCheckRollup StatusCheckRollupStats
		}
	}
}

type LastCommitWithStatusChecks struct {
	Nodes []struct {
		Commit struct {
			Deployments struct {
				Nodes []struct {
					Task        graphql.String
					Description graphql.String
				}
			} `graphql:"deployments(last: 10)"`
			CommitUrl         graphql.String
			StatusCheckRollup struct {
				State    graphql.String
				Contexts struct {
					TotalCount                 graphql.Int
					CheckRunCount              graphql.Int
					CheckRunCountsByState      []ContextCountByState
					StatusContextCount         graphql.Int
					StatusContextCountsByState []ContextCountByState
					Nodes                      []struct {
						Typename      graphql.String `graphql:"__typename"`
						CheckRun      CheckRun       `graphql:"... on CheckRun"`
						StatusContext StatusContext  `graphql:"... on StatusContext"`
					}
				} `graphql:"contexts(last: 100)"`
			}
			// CheckSuites are fetched separately from StatusCheckRollup because
			// workflows awaiting approval (conclusion ACTION_REQUIRED) and workflows
			// still queued have no CheckRun objects yet, so they don’t appear in
			// StatusCheckRollup.contexts.
			CheckSuites CheckSuites `graphql:"checkSuites(last: 20)"`
		}
	}
	TotalCount int
}

type CommentsWithBody struct {
	TotalCount graphql.Int
	Nodes      []Comment
}

type ContextCountByState = struct {
	Count graphql.Int
	State checks.CheckRunState
}

type Commits struct {
	Nodes []struct {
		Commit struct {
			Deployments struct {
				Nodes []struct {
					Task        graphql.String
					Description graphql.String
				}
			} `graphql:"deployments(last: 10)"`
			CommitUrl         graphql.String
			StatusCheckRollup struct {
				State graphql.String
			}
		}
	}
	TotalCount int
}

type Comment struct {
	Author struct {
		Login string
	}
	Body      string
	UpdatedAt time.Time
}

type ReviewComment struct {
	Author struct {
		Login string
	}
	Body      string
	UpdatedAt time.Time
	StartLine int
	Line      int
}

type ReviewComments struct {
	Nodes      []ReviewComment
	TotalCount int
}

type Comments struct {
	TotalCount int
}

type ReviewThreads struct {
	TotalCount int
}

type Review struct {
	Author struct {
		Login string
	}
	Body      string
	State     string
	UpdatedAt time.Time
}

type ReviewsNumber struct {
	TotalCount int
}

type Reviews struct {
	TotalCount int
	Nodes      []Review
}

type ReviewThreadsWithComments struct {
	Nodes []struct {
		Id           string
		IsOutdated   bool
		OriginalLine int
		StartLine    int
		Line         int
		Path         string
		Comments     ReviewComments `graphql:"comments(first: 20)"`
	}
}

type ChangedFile struct {
	Additions  int
	Deletions  int
	Path       string
	ChangeType string
}

type ChangedFiles struct {
	TotalCount int
	Nodes      []ChangedFile
}

type RequestedReviewerUser struct {
	Login string `graphql:"login"`
}

type RequestedReviewerTeam struct {
	Slug string `graphql:"slug"`
	Name string `graphql:"name"`
}

type RequestedReviewerBot struct {
	Login string `graphql:"login"`
}

type RequestedReviewerMannequin struct {
	Login string `graphql:"login"`
}

type ReviewRequestNode struct {
	AsCodeOwner       bool `graphql:"asCodeOwner"`
	RequestedReviewer struct {
		User      RequestedReviewerUser      `graphql:"... on User"`
		Team      RequestedReviewerTeam      `graphql:"... on Team"`
		Bot       RequestedReviewerBot       `graphql:"... on Bot"`
		Mannequin RequestedReviewerMannequin `graphql:"... on Mannequin"`
	} `graphql:"requestedReviewer"`
}

type ReviewRequestsNumber struct {
	TotalCount int
}

type ReviewRequests struct {
	TotalCount int
	Nodes      []ReviewRequestNode
}

func (r ReviewRequestNode) GetReviewerDisplayName() string {
	if r.RequestedReviewer.User.Login != "" {
		return r.RequestedReviewer.User.Login
	}
	if r.RequestedReviewer.Team.Slug != "" {
		return r.RequestedReviewer.Team.Slug
	}
	if r.RequestedReviewer.Bot.Login != "" {
		return r.RequestedReviewer.Bot.Login
	}
	if r.RequestedReviewer.Mannequin.Login != "" {
		return r.RequestedReviewer.Mannequin.Login
	}
	return ""
}

func (r ReviewRequestNode) GetReviewerType() string {
	if r.RequestedReviewer.User.Login != "" {
		return "User"
	}
	if r.RequestedReviewer.Team.Slug != "" {
		return "Team"
	}
	if r.RequestedReviewer.Bot.Login != "" {
		return "Bot"
	}
	if r.RequestedReviewer.Mannequin.Login != "" {
		return "Mannequin"
	}
	return ""
}

func (r ReviewRequestNode) IsTeam() bool {
	return r.RequestedReviewer.Team.Slug != ""
}

type PRLabel struct {
	Color string
	Name  string
}

type PRLabels struct {
	Nodes []Label
}

type MergeStateStatus string

type PageInfo struct {
	HasNextPage bool
	StartCursor string
	EndCursor   string
}

func (data PullRequestData) GetAuthor(theme theme.Theme, showAuthorIcon bool) string {
	author := data.Author.Login
	if showAuthorIcon {
		author += fmt.Sprintf(" %s", GetAuthorRoleIcon(data.AuthorAssociation, theme))
	}
	return author
}

func (data PullRequestData) GetTitle() string {
	return data.Title
}

func (data PullRequestData) GetRepoNameWithOwner() string {
	return data.Repository.NameWithOwner
}

func (data PullRequestData) GetRepoNameAndOwner() (owner, repoName string) {
	return data.Repository.Owner.Login, data.Repository.Name
}

func (data PullRequestData) GetNumber() int {
	return data.Number
}

func (data PullRequestData) GetUrl() string {
	return data.Url
}

func (data PullRequestData) GetUpdatedAt() time.Time {
	return data.UpdatedAt
}

func (data PullRequestData) GetCreatedAt() time.Time {
	return data.CreatedAt
}

// ToPullRequestData converts EnrichedPullRequestData to PullRequestData
// This is useful when we fetch a single PR and need basic PR fields
func (e EnrichedPullRequestData) ToPullRequestData() PullRequestData {
	return PullRequestData{
		Number:            e.Number,
		Title:             e.Title,
		Author:            e.Author,
		AuthorAssociation: e.AuthorAssociation,
		UpdatedAt:         e.UpdatedAt,
		CreatedAt:         e.CreatedAt,
		Url:               e.Url,
		State:             e.State,
		Mergeable:         e.Mergeable,
		ReviewDecision:    e.ReviewDecision,
		Additions:         e.Additions,
		Deletions:         e.Deletions,
		HeadRefName:       e.HeadRefName,
		BaseRefName:       e.BaseRefName,
		HeadRepository:    e.HeadRepository,
		HeadRef:           e.HeadRef,
		Repository:        e.Repository,
		Assignees:         e.Assignees,
		IsDraft:           e.IsDraft,
		Labels:            e.Labels,
		// Note: Comments, ReviewThreads, Reviews, ReviewRequests, Commits
		// have different types in EnrichedPullRequestData vs PullRequestData
		// We leave them as zero values since the enriched data will be used instead
	}
}

func makePullRequestsQuery(query string) string {
	// Sort by last update unless the section asks for another order.
	if hasSortQualifier(query) {
		return fmt.Sprintf("is:pr archived:false %s", query)
	}
	return fmt.Sprintf("is:pr archived:false %s sort:updated", query)
}

type PullRequestsResponse struct {
	Prs        []PullRequestData
	TotalCount int
	PageInfo   PageInfo
}

var (
	client       *gh.GraphQLClient
	cachedClient *gh.GraphQLClient
)

func SetClient(c *gh.GraphQLClient) {
	client = c
	cachedClient = c
}

// ClearEnrichmentCache clears the cached GraphQL client used for fetching
// enriched PR/Issue data. Call this when refreshing to ensure fresh data.
func ClearEnrichmentCache() {
	cachedClient = nil
}

// IsEnrichmentCacheCleared returns true if the enrichment cache is cleared.
// This is primarily for testing purposes.
func IsEnrichmentCacheCleared() bool {
	return cachedClient == nil
}

func FetchPullRequests(query string, limit int, pageInfo *PageInfo) (PullRequestsResponse, error) {
	var err error
	if client == nil {
		if config.IsFeatureEnabled(config.FF_MOCK_DATA) {
			log.Info("using mock data", "server", "https://localhost:3000")
			http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{
				InsecureSkipVerify: true,
			}
			client, err = gh.NewGraphQLClient(
				gh.ClientOptions{Host: "localhost:3000", AuthToken: "fake-token"},
			)
		} else {
			level := os.Getenv("LOG_LEVEL")
			opts := gh.ClientOptions{}
			if level == "debug" {
				logger := NewHTTPLogger(0)
				opts.Log = &logger
				opts.LogVerboseHTTP = true
				opts.LogColorize = true
			}
			client, err = gh.NewGraphQLClient(opts)
		}
	}

	if err != nil {
		return PullRequestsResponse{}, err
	}

	query, localFilters := splitLocalPRQualifiers(query)

	var endCursor *string
	if pageInfo != nil {
		endCursor = &pageInfo.EndCursor
	}
	res, err := searchPullRequests(query, limit, endCursor)
	if err != nil || len(localFilters) == 0 {
		return res, err
	}

	// Filtered locally: gather pages until enough PRs match, so that the
	// section's count stays right.
	var prs []PullRequestData
	for page := 0; ; page++ {
		for _, pr := range res.Prs {
			if matchesLocalFilters(pr, localFilters) {
				prs = append(prs, pr)
			}
		}
		if !res.PageInfo.HasNextPage || len(prs) >= limit || page >= 4 {
			break
		}
		cursor := res.PageInfo.EndCursor
		if res, err = searchPullRequests(query, limit, &cursor); err != nil {
			return PullRequestsResponse{}, err
		}
	}
	if prs == nil {
		prs = []PullRequestData{}
	}
	return PullRequestsResponse{
		Prs:        prs,
		TotalCount: len(prs),
		PageInfo:   PageInfo{HasNextPage: false, EndCursor: res.PageInfo.EndCursor},
	}, nil
}

func searchPullRequests(query string, limit int, endCursor *string) (PullRequestsResponse, error) {
	type searchResult struct {
		Nodes []struct {
			PullRequest PullRequestData `graphql:"... on PullRequest"`
		}
		IssueCount int
		PageInfo   PageInfo
	}
	variables := map[string]any{
		"query":     graphql.String(makePullRequestsQuery(query)),
		"limit":     graphql.Int(limit),
		"endCursor": (*graphql.String)(endCursor),
	}
	log.Debug("Fetching PRs", "query", query, "limit", limit, "endCursor", endCursor)

	// Plain search does not support OR and parentheses: use the advanced
	// search for queries that need them.
	var result searchResult
	var err error
	if usesAdvancedSearch(query) {
		var queryResult struct {
			Search searchResult `graphql:"search(type: ISSUE_ADVANCED, first: $limit, after: $endCursor, query: $query)"`
		}
		err = client.Query("SearchPullRequestsAdvanced", &queryResult, variables)
		result = queryResult.Search
	} else {
		var queryResult struct {
			Search searchResult `graphql:"search(type: ISSUE, first: $limit, after: $endCursor, query: $query)"`
		}
		err = client.Query("SearchPullRequests", &queryResult, variables)
		result = queryResult.Search
	}
	if err != nil {
		return PullRequestsResponse{}, err
	}
	log.Info("Successfully fetched PRs", "count", result.IssueCount)

	prs := make([]PullRequestData, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		prs = append(prs, node.PullRequest)
	}
	return PullRequestsResponse{
		Prs:        prs,
		TotalCount: result.IssueCount,
		PageInfo:   result.PageInfo,
	}, nil
}

func usesAdvancedSearch(query string) bool {
	return strings.Contains(query, " OR ") || strings.Contains(query, " AND ") ||
		strings.ContainsAny(query, "()")
}

type prFilter func(PullRequestData) bool

// splitLocalPRQualifiers removes the qualifiers GitHub's search does not
// support and that gh-dash applies itself:
//   - no:reviewer      no review requested and no review submitted
//   - sprint:current   closes an issue of the scrum board's current sprint
func splitLocalPRQualifiers(query string) (string, []prFilter) {
	var kept []string
	var filters []prFilter
	for _, tok := range strings.Fields(query) {
		switch strings.ToLower(tok) {
		case "no:reviewer", "no:reviewers":
			filters = append(filters, func(pr PullRequestData) bool {
				return pr.ReviewRequests.TotalCount == 0 && pr.Reviews.TotalCount == 0
			})
		case "sprint:current":
			filters = append(filters, func(pr PullRequestData) bool {
				return IsPRInCurrentSprint(pr.Repository.NameWithOwner, pr.Number)
			})
		default:
			kept = append(kept, tok)
		}
	}
	return strings.Join(kept, " "), filters
}

func matchesLocalFilters(pr PullRequestData, filters []prFilter) bool {
	for _, f := range filters {
		if !f(pr) {
			return false
		}
	}
	return true
}

// HasBoardPRQualifier reports whether a PR filter depends on the scrum board.
func HasBoardPRQualifier(query string) bool {
	return strings.Contains(strings.ToLower(query), "sprint:current")
}

func FetchPullRequest(prUrl string) (EnrichedPullRequestData, error) {
	var err error
	if client == nil {
		client, err = gh.DefaultGraphQLClient()
		if err != nil {
			return EnrichedPullRequestData{}, err
		}
	}

	var queryResult struct {
		Resource struct {
			PullRequest EnrichedPullRequestData `graphql:"... on PullRequest"`
		} `graphql:"resource(url: $url)"`
	}
	parsedUrl, err := url.Parse(prUrl)
	if err != nil {
		return EnrichedPullRequestData{}, err
	}
	variables := map[string]any{
		"url": githubv4.URI{URL: parsedUrl},
	}
	log.Debug("Fetching PR", "url", prUrl)
	err = client.Query("FetchPullRequest", &queryResult, variables)
	if err != nil {
		return EnrichedPullRequestData{}, err
	}
	log.Info("Successfully fetched PR", "url", prUrl)

	return queryResult.Resource.PullRequest, nil
}
