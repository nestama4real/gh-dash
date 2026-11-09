package data

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/log/v2"
	gh "github.com/cli/go-gh/v2/pkg/api"
)

// ProjectFieldOption is one option of a single-select project field.
type ProjectFieldOption struct {
	Id          string
	Name        string
	Color       string
	Description string
}

// ProjectIteration is one iteration of an iteration project field.
type ProjectIteration struct {
	Id        string
	Title     string
	StartDate string
	Duration  int
}

// ProjectField describes a field of a GitHub Project (v2).
type ProjectField struct {
	Id         string
	Name       string
	DataType   string // TITLE, ASSIGNEES, SINGLE_SELECT, NUMBER, TEXT, DATE, ITERATION, ...
	Options    []ProjectFieldOption
	Iterations []ProjectIteration
}

func (f ProjectField) IsEditable() bool {
	switch f.DataType {
	case "SINGLE_SELECT", "NUMBER", "TEXT", "DATE", "ITERATION":
		return true
	}
	return false
}

func (f ProjectField) OptionByName(name string) (ProjectFieldOption, bool) {
	for _, o := range f.Options {
		if o.Name == name {
			return o, true
		}
	}
	return ProjectFieldOption{}, false
}

// ProjectView is a saved view of a project (a tab on github.com).
type ProjectView struct {
	Id            string
	Name          string
	Number        int
	Layout        string
	Filter        string
	VisibleFields []string
}

// ProjectFieldValue is the value of one field on one project item.
type ProjectFieldValue struct {
	FieldName string
	OptionId  string // single select option id or iteration id
	Name      string // single select option name or iteration title
	Number    *float64
	Text      string
	Date      string
}

// String renders the value the way it would be typed in a filter.
func (v ProjectFieldValue) String() string {
	switch {
	case v.Name != "":
		return v.Name
	case v.Number != nil:
		return FormatProjectNumber(*v.Number)
	case v.Text != "":
		return v.Text
	default:
		return v.Date
	}
}

func FormatProjectNumber(n float64) string {
	if n == float64(int64(n)) {
		return fmt.Sprintf("%d", int64(n))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", n), "0"), ".")
}

// ProjectItem is a card of the project.
type ProjectItem struct {
	Id          string
	ContentType string // Issue, PullRequest, DraftIssue
	Content     IssueData
	FieldValues map[string]ProjectFieldValue
}

func (i *ProjectItem) Value(field string) (ProjectFieldValue, bool) {
	v, ok := i.FieldValues[field]
	return v, ok
}

// ProjectData is a whole project: metadata and all its (non archived) items.
type ProjectData struct {
	Id     string
	Title  string
	Url    string
	Fields []ProjectField
	Views  []ProjectView
	Items  []ProjectItem
}

func (p *ProjectData) Field(name string) (ProjectField, bool) {
	for _, f := range p.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return ProjectField{}, false
}

const projectIssueFields = `
  number title body state url createdAt updatedAt authorAssociation
  author { login }
  repository { name nameWithOwner isArchived owner { login } }
  assignees(first: 10) { nodes { login } }
  comments(last: 15) { totalCount nodes { author { login } body updatedAt } }
  reactions(first: 1) { totalCount }
  labels(first: 20) { nodes { color name description } }
`

const projectFieldName = `field { ... on ProjectV2FieldCommon { name } }`

var projectQuery = `
query BoardProject($owner: String!, $number: Int!, $first: Int!, $after: String, $withMeta: Boolean!) {
  repositoryOwner(login: $owner) {
    ... on ProjectV2Owner {
      projectV2(number: $number) {
        id title url
        fields(first: 50) @include(if: $withMeta) {
          nodes {
            ... on ProjectV2FieldCommon { id name dataType }
            ... on ProjectV2SingleSelectField { options { id name color description } }
            ... on ProjectV2IterationField {
              configuration { iterations { id title startDate duration } }
            }
          }
        }
        views(first: 30) @include(if: $withMeta) {
          nodes {
            id name number layout filter
            visibleFields(first: 50) { nodes { ... on ProjectV2FieldCommon { name } } }
          }
        }
        items(first: $first, after: $after) {
          pageInfo { hasNextPage endCursor }
          nodes {
            id isArchived type
            fieldValues(first: 50) {
              nodes {
                __typename
                ... on ProjectV2ItemFieldSingleSelectValue { optionId name ` + projectFieldName + ` }
                ... on ProjectV2ItemFieldNumberValue { number ` + projectFieldName + ` }
                ... on ProjectV2ItemFieldTextValue { text ` + projectFieldName + ` }
                ... on ProjectV2ItemFieldDateValue { date ` + projectFieldName + ` }
                ... on ProjectV2ItemFieldIterationValue { iterationId title ` + projectFieldName + ` }
              }
            }
            content {
              __typename
              ... on Issue { ` + projectIssueFields + ` }
              ... on PullRequest { ` + projectIssueFields + ` }
              ... on DraftIssue {
                title body createdAt updatedAt
                assignees(first: 10) { nodes { login } }
              }
            }
          }
        }
      }
    }
  }
}`

type rawFieldName struct {
	Name string
}

type rawProjectResponse struct {
	RepositoryOwner *struct {
		ProjectV2 *struct {
			Id     string
			Title  string
			Url    string
			Fields struct {
				Nodes []struct {
					Id            string
					Name          string
					DataType      string
					Options       []ProjectFieldOption
					Configuration *struct {
						Iterations []ProjectIteration
					}
				}
			}
			Views struct {
				Nodes []struct {
					Id            string
					Name          string
					Number        int
					Layout        string
					Filter        string
					VisibleFields struct {
						Nodes []rawFieldName
					}
				}
			}
			Items struct {
				PageInfo PageInfo
				Nodes    []struct {
					Id          string
					IsArchived  bool
					Type        string
					FieldValues struct {
						Nodes []struct {
							Typename    string `json:"__typename"`
							OptionId    string
							IterationId string
							Name        string
							Title       string
							Number      *float64
							Text        string
							Date        string
							Field       rawFieldName
						}
					}
					Content *struct {
						Typename string `json:"__typename"`
						IssueData
					}
				}
			}
		}
	}
}

// FetchProjectPage fetches one page of a project's items. The first page
// (after == nil) also carries the project's fields and views. next is nil on
// the last page.
func FetchProjectPage(owner string, number int, after *string) (*ProjectData, *string, error) {
	client, err := gh.DefaultGraphQLClient()
	if err != nil {
		return nil, nil, err
	}

	withMeta := after == nil
	var resp rawProjectResponse
	vars := map[string]any{
		"owner":    owner,
		"number":   number,
		"first":    100,
		"after":    after,
		"withMeta": withMeta,
	}
	start := time.Now()
	if err := client.Do(projectQuery, vars, &resp); err != nil {
		return nil, nil, err
	}
	if resp.RepositoryOwner == nil || resp.RepositoryOwner.ProjectV2 == nil {
		return nil, nil, fmt.Errorf("project %s/%d not found", owner, number)
	}
	p := resp.RepositoryOwner.ProjectV2
	project := &ProjectData{Id: p.Id, Title: p.Title, Url: p.Url}
	if withMeta {
		for _, f := range p.Fields.Nodes {
			field := ProjectField{
				Id: f.Id, Name: f.Name, DataType: f.DataType, Options: f.Options,
			}
			if f.Configuration != nil {
				field.Iterations = f.Configuration.Iterations
			}
			project.Fields = append(project.Fields, field)
		}
		for _, v := range p.Views.Nodes {
			view := ProjectView{
				Id: v.Id, Name: v.Name, Number: v.Number, Layout: v.Layout, Filter: v.Filter,
			}
			for _, vf := range v.VisibleFields.Nodes {
				view.VisibleFields = append(view.VisibleFields, vf.Name)
			}
			project.Views = append(project.Views, view)
		}
	}

	for _, n := range p.Items.Nodes {
		if n.IsArchived || n.Content == nil {
			continue
		}
		item := ProjectItem{
			Id:          n.Id,
			ContentType: n.Content.Typename,
			Content:     n.Content.IssueData,
			FieldValues: map[string]ProjectFieldValue{},
		}
		if item.ContentType == "DraftIssue" {
			item.Content.State = "OPEN"
		}
		for _, fv := range n.FieldValues.Nodes {
			if fv.Field.Name == "" {
				continue
			}
			val := ProjectFieldValue{FieldName: fv.Field.Name}
			switch fv.Typename {
			case "ProjectV2ItemFieldSingleSelectValue":
				val.OptionId, val.Name = fv.OptionId, fv.Name
			case "ProjectV2ItemFieldIterationValue":
				val.OptionId, val.Name = fv.IterationId, fv.Title
			case "ProjectV2ItemFieldNumberValue":
				val.Number = fv.Number
			case "ProjectV2ItemFieldTextValue":
				val.Text = fv.Text
			case "ProjectV2ItemFieldDateValue":
				val.Date = fv.Date
			default:
				continue
			}
			item.FieldValues[fv.Field.Name] = val
		}
		project.Items = append(project.Items, item)
	}

	log.Debug("Fetched project page", "owner", owner, "number", number,
		"items", len(project.Items), "took", time.Since(start))
	if !p.Items.PageInfo.HasNextPage {
		return project, nil, nil
	}
	cursor := p.Items.PageInfo.EndCursor
	return project, &cursor, nil
}

func projectCachePath(owner string, number int) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gh-dash", fmt.Sprintf("project-%s-%d.json", owner, number)), nil
}

type cachedProject struct {
	FetchedAt time.Time
	Project   *ProjectData
}

// LoadCachedProject returns the last project saved by SaveCachedProject.
func LoadCachedProject(owner string, number int) (*ProjectData, time.Time, bool) {
	path, err := projectCachePath(owner, number)
	if err != nil {
		return nil, time.Time{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	var c cachedProject
	if err := json.Unmarshal(b, &c); err != nil || c.Project == nil {
		log.Warn("Ignoring unreadable project cache", "path", path, "err", err)
		return nil, time.Time{}, false
	}
	return c.Project, c.FetchedAt, true
}

func SaveCachedProject(owner string, number int, p *ProjectData, fetchedAt time.Time) {
	path, err := projectCachePath(owner, number)
	if err != nil {
		return
	}
	b, err := json.Marshal(cachedProject{FetchedAt: fetchedAt, Project: p})
	if err != nil {
		log.Warn("Failed encoding project cache", "err", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Warn("Failed writing project cache", "err", err)
		return
	}
	_ = os.Rename(tmp, path)
}

// ProjectFieldUpdate is the new value of a field. A nil Value clears the field.
// Value keys follow the ProjectV2FieldValue input type: singleSelectOptionId,
// number, text, date or iterationId.
type ProjectFieldUpdate struct {
	ProjectId string
	ItemId    string
	FieldId   string
	Value     map[string]any
}

func UpdateProjectItemField(u ProjectFieldUpdate) error {
	client, err := gh.DefaultGraphQLClient()
	if err != nil {
		return err
	}

	var resp struct{}
	if u.Value == nil {
		return client.Do(`mutation ClearItemField($project: ID!, $item: ID!, $field: ID!) {
  clearProjectV2ItemFieldValue(input: {projectId: $project, itemId: $item, fieldId: $field}) {
    projectV2Item { id }
  }
}`, map[string]any{"project": u.ProjectId, "item": u.ItemId, "field": u.FieldId}, &resp)
	}

	return client.Do(`mutation UpdateItemField($project: ID!, $item: ID!, $field: ID!, $value: ProjectV2FieldValue!) {
  updateProjectV2ItemFieldValue(input: {projectId: $project, itemId: $item, fieldId: $field, value: $value}) {
    projectV2Item { id }
  }
}`, map[string]any{
		"project": u.ProjectId, "item": u.ItemId, "field": u.FieldId, "value": u.Value,
	}, &resp)
}
