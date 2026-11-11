package tasks

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEditAsAPICalls(t *testing.T) {
	calls, ok := editAsAPICalls("me", []string{"pr", "edit", "12", "-R", "o/r",
		"--add-reviewer", "alice,o/core", "--remove-label", "needs review", "--add-label", "bug", "--add-assignee", "@me"})
	require.True(t, ok)
	require.Equal(t, [][]string{
		{"api", "-X", "DELETE", "--silent", "repos/o/r/issues/12/labels/needs%20review"},
		{"api", "-X", "POST", "--silent", "repos/o/r/issues/12/labels", "-f", "labels[]=bug"},
		{"api", "-X", "POST", "--silent", "repos/o/r/issues/12/assignees", "-f", "assignees[]=me"},
		{"api", "-X", "POST", "--silent", "repos/o/r/pulls/12/requested_reviewers", "-f", "reviewers[]=alice", "-f", "team_reviewers[]=core"},
	}, calls)

	_, ok = editAsAPICalls("me", []string{"pr", "merge", "12", "-R", "o/r"})
	require.False(t, ok)
}
