package boardsection

import (
	"testing"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
)

func num(f float64) *float64 { return &f }

func testProject() *data.ProjectData {
	return &data.ProjectData{
		Fields: []data.ProjectField{
			{Id: "F1", Name: "Status", DataType: "SINGLE_SELECT", Options: []data.ProjectFieldOption{
				{Id: "o1", Name: "Sprint Backlog"}, {Id: "o2", Name: "In Review"},
				{Id: "o3", Name: "Done in 2"}, {Id: "o4", Name: "Done in 1"},
			}},
			{Id: "F2", Name: "Sprint", DataType: "NUMBER"},
			{Id: "F3", Name: "Task Type", DataType: "SINGLE_SELECT"},
		},
	}
}

func item(title, status, taskType string, sprint float64, assignee string, state string) data.ProjectItem {
	it := data.ProjectItem{
		ContentType: "Issue",
		FieldValues: map[string]data.ProjectFieldValue{
			"Sprint": {FieldName: "Sprint", Number: num(sprint)},
		},
	}
	it.Content.Title = title
	it.Content.State = state
	if status != "" {
		it.FieldValues["Status"] = data.ProjectFieldValue{FieldName: "Status", Name: status}
	}
	if taskType != "" {
		it.FieldValues["Task Type"] = data.ProjectFieldValue{FieldName: "Task Type", Name: taskType}
	}
	if assignee != "" {
		it.Content.Assignees.Nodes = []data.Assignee{{Login: assignee}}
	}
	return it
}

func TestFilterMatching(t *testing.T) {
	p := testProject()
	fc := newFilterContext(p, "me")
	a := item("Edit a team name", "In Review", "Frontend", 5, "me", "OPEN")
	b := item("Fix flaky tests", "Sprint Backlog", "Other", 4, "bob", "CLOSED")

	cases := []struct {
		filter string
		a, b   bool
	}{
		{"", true, true},
		{"sprint:5", true, false},
		{"Sprint:5,4", true, true},
		{"sprint:>4", true, false},
		{"sprint:4..5", true, true},
		{"-sprint:5", false, true},
		{"assignee:@me", true, false},
		{"assignee:bob", false, true},
		{`status:"In Review"`, true, false},
		{`-status:"In Review"`, false, true},
		{"task-type:frontend", true, false},
		{`"task type":Other`, false, true},
		{"is:open", true, false},
		{"no:status", false, false},
		{"has:task-type", true, true},
		{"flaky", false, true},
		{"team sprint:5", true, false},
		{"unknown:whatever", true, true},
	}
	for _, c := range cases {
		terms := parseFilter(c.filter)
		if got := fc.matchesAll(&a, terms); got != c.a {
			t.Errorf("%q on a: got %v, want %v", c.filter, got, c.a)
		}
		if got := fc.matchesAll(&b, terms); got != c.b {
			t.Errorf("%q on b: got %v, want %v", c.filter, got, c.b)
		}
	}
}
