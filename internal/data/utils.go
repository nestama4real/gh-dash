package data

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"

	"github.com/dlvhdr/gh-dash/v4/internal/tui/theme"
)

type RowData interface {
	GetRepoNameWithOwner() string
	GetTitle() string
	GetNumber() int
	GetUrl() string
	GetUpdatedAt() time.Time
}

func GetAuthorRoleIcon(role string, theme theme.Theme) string {
	// https://docs.github.com/en/graphql/reference/enums#commentauthorassociation
	switch role {
	case "FIRST_TIMER", "FIRST_TIME_CONTRIBUTOR", "NONE":
		return lipgloss.NewStyle().
			Foreground(compat.AdaptiveColor(theme.NewContributorIconColor)).
			Render(theme.NewContributorIcon)
	case "COLLABORATOR":
		return lipgloss.NewStyle().
			Foreground(compat.AdaptiveColor(theme.CollaboratorIconColor)).
			Render(theme.CollaboratorIcon)
	case "CONTRIBUTOR":
		return lipgloss.NewStyle().
			Foreground(compat.AdaptiveColor(theme.ContributorIconColor)).
			Render(theme.ContributorIcon)
	case "MEMBER":
		return lipgloss.NewStyle().
			Foreground(compat.AdaptiveColor(theme.MemberIconColor)).
			Render(theme.MemberIcon)
	case "OWNER":
		return lipgloss.NewStyle().
			Foreground(compat.AdaptiveColor(theme.OwnerIconColor)).
			Render(theme.OwnerIcon)
	default:
		return lipgloss.NewStyle().
			Foreground(compat.AdaptiveColor(theme.UnknownRoleIconColor)).
			Render(theme.UnknownRoleIcon)
	}
}

// hasSortQualifier reports whether a search query sets its own order, e.g.
// sort:created-desc.
func hasSortQualifier(query string) bool {
	for _, tok := range strings.Fields(query) {
		if strings.HasPrefix(strings.ToLower(tok), "sort:") {
			return true
		}
	}
	return false
}
