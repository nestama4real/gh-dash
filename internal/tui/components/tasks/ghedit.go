package tasks

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// RunGh runs a gh command and returns its error message, not just its exit
// status. `gh pr edit` and `gh issue edit` are done with the REST API
// instead: older gh versions (before 2.49) fail on them, querying the
// Projects (classic) that GitHub removed.
func RunGh(user string, args []string) error {
	if calls, ok := editAsAPICalls(user, args); ok {
		for _, call := range calls {
			if err := runGh(call); err != nil {
				return err
			}
		}
		return nil
	}
	return runGh(args)
}

func runGh(args []string) error {
	c := exec.Command("gh", args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// editAsAPICalls turns `gh pr|issue edit N -R owner/repo --add-xxx v ...`
// into REST calls. ok is false for anything else.
func editAsAPICalls(user string, args []string) ([][]string, bool) {
	if len(args) < 5 || (args[0] != "pr" && args[0] != "issue") || args[1] != "edit" || args[3] != "-R" {
		return nil, false
	}
	number, repo := args[2], args[4]
	issues := fmt.Sprintf("repos/%s/issues/%s", repo, number)
	var add, remove, reviewers, teams, addLabels []string
	var calls [][]string
	me := func(v string) string {
		if v == "@me" && user != "" {
			return user
		}
		return strings.TrimPrefix(v, "@")
	}
	rest := args[5:]
	if len(rest)%2 != 0 {
		return nil, false
	}
	for i := 0; i < len(rest); i += 2 {
		flag, v := rest[i], rest[i+1]
		switch flag {
		case "--add-assignee":
			add = append(add, me(v))
		case "--remove-assignee":
			remove = append(remove, me(v))
		case "--add-reviewer":
			for _, r := range strings.Split(v, ",") {
				if r = strings.TrimSpace(r); r == "" {
					continue
				}
				if strings.Contains(r, "/") {
					_, team, _ := strings.Cut(r, "/")
					teams = append(teams, team)
				} else {
					reviewers = append(reviewers, me(r))
				}
			}
		case "--add-label":
			addLabels = append(addLabels, v)
		case "--remove-label":
			calls = append(calls, []string{"api", "-X", "DELETE", "--silent",
				issues + "/labels/" + url.PathEscape(v)})
		default:
			return nil, false
		}
	}
	withList := func(method, path, field string, values []string) []string {
		call := []string{"api", "-X", method, "--silent", path}
		for _, v := range values {
			call = append(call, "-f", field+"[]="+v)
		}
		return call
	}
	if len(addLabels) > 0 {
		calls = append(calls, withList("POST", issues+"/labels", "labels", addLabels))
	}
	if len(add) > 0 {
		calls = append(calls, withList("POST", issues+"/assignees", "assignees", add))
	}
	if len(remove) > 0 {
		calls = append(calls, withList("DELETE", issues+"/assignees", "assignees", remove))
	}
	if len(reviewers) > 0 || len(teams) > 0 {
		call := withList("POST", fmt.Sprintf("repos/%s/pulls/%s/requested_reviewers", repo, number), "reviewers", reviewers)
		for _, t := range teams {
			call = append(call, "-f", "team_reviewers[]="+t)
		}
		calls = append(calls, call)
	}
	return calls, true
}
