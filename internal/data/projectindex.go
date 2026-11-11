package data

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// The project index lets the PRs and issues views show the scrum board's
// fields without fetching them again. It is read from fetch goroutines, hence
// the lock and the copied data.
var (
	projectIndexMu sync.RWMutex
	projectIndex   = struct {
		items         map[string]map[string]ProjectFieldValue // "owner/repo#number"
		fields        map[string]ProjectField
		statusField   string
		currentSprint *float64
		sprintField   string
		// prs linked to an issue, keyed like items, valued by the issues' keys
		linkedPRs map[string][]string
		titles    map[string]string
	}{}
)

var sprintFilterRe = regexp.MustCompile(`(?i)(?:^|\s)([\w-]*sprint[\w-]*):(\d+(?:\.\d+)?)\b`)

func indexKey(repoNameWithOwner string, number int) string {
	return fmt.Sprintf("%s#%d", strings.ToLower(repoNameWithOwner), number)
}

// SetProjectIndex rebuilds the index from a project.
func SetProjectIndex(p *ProjectData, statusField string) {
	items := map[string]map[string]ProjectFieldValue{}
	linked := map[string][]string{}
	fields := map[string]ProjectField{}
	titles := map[string]string{}
	var current *float64
	sprintField := ""
	if p != nil {
		for _, f := range p.Fields {
			fields[strings.ToLower(f.Name)] = f
		}
		for i := range p.Items {
			it := &p.Items[i]
			if it.Content.Number == 0 {
				continue
			}
			key := indexKey(it.Content.Repository.NameWithOwner, it.Content.Number)
			values := make(map[string]ProjectFieldValue, len(it.FieldValues))
			for k, v := range it.FieldValues {
				values[k] = v
			}
			items[key] = values
			titles[key] = it.Content.Title
			for _, pr := range it.Content.ClosedByPullRequestsReferences.Nodes {
				prKey := prKeyFromUrl(pr.Url, pr.Number)
				linked[prKey] = append(linked[prKey], key)
			}
		}
		// The current sprint is the one the "Current Sprint" view filters on.
		for _, v := range p.Views {
			if !strings.Contains(strings.ToLower(v.Name), "current") {
				continue
			}
			if m := sprintFilterRe.FindStringSubmatch(v.Filter); m != nil {
				if n, err := strconv.ParseFloat(m[2], 64); err == nil {
					current, sprintField = &n, m[1]
					break
				}
			}
		}
		if current != nil {
			for _, f := range p.Fields {
				if strings.EqualFold(strings.ReplaceAll(f.Name, " ", "-"), sprintField) {
					sprintField = f.Name
				}
			}
		}
	}

	projectIndexMu.Lock()
	defer projectIndexMu.Unlock()
	projectIndex.items = items
	projectIndex.fields = fields
	projectIndex.statusField = statusField
	projectIndex.currentSprint = current
	projectIndex.sprintField = sprintField
	projectIndex.linkedPRs = linked
	projectIndex.titles = titles
}

// LookupProjectTitle returns the title of an item of the indexed project.
func LookupProjectTitle(repoNameWithOwner string, number int) (string, bool) {
	projectIndexMu.RLock()
	defer projectIndexMu.RUnlock()
	t, ok := projectIndex.titles[indexKey(repoNameWithOwner, number)]
	return t, ok
}

// prKeyFromUrl turns https://github.com/owner/repo/pull/12 into owner/repo#12.
func prKeyFromUrl(url string, number int) string {
	parts := strings.Split(strings.TrimPrefix(url, "https://github.com/"), "/")
	if len(parts) >= 2 {
		return indexKey(parts[0]+"/"+parts[1], number)
	}
	return indexKey("", number)
}

// HasProjectIndex reports whether a project has been indexed.
func HasProjectIndex() bool {
	projectIndexMu.RLock()
	defer projectIndexMu.RUnlock()
	return projectIndex.items != nil
}

// LookupProjectValue returns the value of a project field of an issue/PR.
func LookupProjectValue(repoNameWithOwner string, number int, field string) (ProjectFieldValue, bool) {
	projectIndexMu.RLock()
	defer projectIndexMu.RUnlock()
	values, ok := projectIndex.items[indexKey(repoNameWithOwner, number)]
	if !ok {
		return ProjectFieldValue{}, false
	}
	if f, ok := projectIndex.fields[strings.ToLower(field)]; ok {
		field = f.Name
	}
	v, ok := values[field]
	return v, ok
}

// IsInProject reports whether the issue is an item of the indexed project.
func IsInProject(repoNameWithOwner string, number int) bool {
	projectIndexMu.RLock()
	defer projectIndexMu.RUnlock()
	_, ok := projectIndex.items[indexKey(repoNameWithOwner, number)]
	return ok
}

// LookupProjectField returns a field definition, e.g. for option colors.
func LookupProjectField(name string) (ProjectField, bool) {
	projectIndexMu.RLock()
	defer projectIndexMu.RUnlock()
	f, ok := projectIndex.fields[strings.ToLower(name)]
	return f, ok
}

// ProjectStatusField is the name of the field used for the board columns.
func ProjectStatusField() string {
	projectIndexMu.RLock()
	defer projectIndexMu.RUnlock()
	return projectIndex.statusField
}

// IsPRInCurrentSprint reports whether the PR closes an issue of the current
// sprint of the indexed project.
func IsPRInCurrentSprint(repoNameWithOwner string, number int) bool {
	projectIndexMu.RLock()
	defer projectIndexMu.RUnlock()
	if projectIndex.currentSprint == nil {
		return false
	}
	for _, issueKey := range projectIndex.linkedPRs[indexKey(repoNameWithOwner, number)] {
		v, ok := projectIndex.items[issueKey][projectIndex.sprintField]
		if ok && v.Number != nil && *v.Number == *projectIndex.currentSprint {
			return true
		}
	}
	return false
}
