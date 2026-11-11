package keys

import (
	"charm.land/bubbles/v2/key"
)

// WorkKeyMap holds the keys of the "Current Work" view (git worktrees).
type WorkKeyMap struct {
	Review    key.Binding
	OpenIssue key.Binding
}

var WorkKeys = WorkKeyMap{
	Review: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "review the changes"),
	),
	OpenIssue: key.NewBinding(
		key.WithKeys("I"),
		key.WithHelp("I", "open linked issue"),
	),
}

func WorkFullHelp() []key.Binding {
	return []key.Binding{
		WorkKeys.Review,
		WorkKeys.OpenIssue,
	}
}
