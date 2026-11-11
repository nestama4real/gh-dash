package keys

import (
	"charm.land/bubbles/v2/key"
)

// WorkKeyMap holds the keys of the "Current Work" view (git worktrees).
type WorkKeyMap struct {
	OpenIssue key.Binding
}

var WorkKeys = WorkKeyMap{
	OpenIssue: key.NewBinding(
		key.WithKeys("I"),
		key.WithHelp("I", "open linked issue"),
	),
}

func WorkFullHelp() []key.Binding {
	return []key.Binding{
		WorkKeys.OpenIssue,
	}
}
