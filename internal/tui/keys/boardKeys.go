package keys

import (
	"charm.land/bubbles/v2/key"
)

// BoardKeyMap holds the keys specific to the scrum board view. Everything
// else (j/k, tab/shift+tab for tabs, o, p, r, /, y, issue actions...) is
// shared with the other views.
type BoardKeyMap struct {
	PrevColumn    key.Binding
	NextColumn    key.Binding
	MoveLeft      key.Binding
	MoveRight     key.Binding
	EditFields    key.Binding
	EditEstimate  key.Binding
	EditActual    key.Binding
	ToggleOldDone key.Binding
	Preview       key.Binding
	ClosePreview  key.Binding
}

var BoardKeys = BoardKeyMap{
	PrevColumn: key.NewBinding(
		key.WithKeys("left", "h", "["),
		key.WithHelp("←/h/[", "previous column"),
	),
	NextColumn: key.NewBinding(
		key.WithKeys("right", "l", "]"),
		key.WithHelp("→/l/]", "next column"),
	),
	MoveLeft: key.NewBinding(
		key.WithKeys("<", "shift+left"),
		key.WithHelp("</⇧←", "move card left"),
	),
	MoveRight: key.NewBinding(
		key.WithKeys(">", "shift+right"),
		key.WithHelp(">/⇧→", "move card right"),
	),
	EditFields: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "edit fields"),
	),
	EditEstimate: key.NewBinding(
		key.WithKeys("t"),
		key.WithHelp("t", "set estimated time"),
	),
	EditActual: key.NewBinding(
		key.WithKeys("T"),
		key.WithHelp("T", "set actual time"),
	),
	ToggleOldDone: key.NewBinding(
		key.WithKeys("z"),
		key.WithHelp("z", "fold/unfold old done"),
	),
	Preview: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "toggle preview"),
	),
	ClosePreview: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "close preview"),
	),
}

func BoardFullHelp() []key.Binding {
	return []key.Binding{
		BoardKeys.PrevColumn,
		BoardKeys.NextColumn,
		BoardKeys.MoveLeft,
		BoardKeys.MoveRight,
		BoardKeys.EditFields,
		BoardKeys.EditEstimate,
		BoardKeys.EditActual,
		BoardKeys.ToggleOldDone,
		BoardKeys.Preview,
		IssueKeys.Label,
		IssueKeys.Assign,
		IssueKeys.Unassign,
		IssueKeys.Comment,
		IssueKeys.Close,
		IssueKeys.Reopen,
	}
}
