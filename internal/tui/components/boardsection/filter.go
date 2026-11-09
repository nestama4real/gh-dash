package boardsection

import (
	"strconv"
	"strings"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
)

// filterTerm is one space separated term of a GitHub Projects filter, e.g.
// `sprint:5`, `-status:"In Review",Done`, `assignee:@me`, `no:priority` or
// plain text matched against the title.
type filterTerm struct {
	negate bool
	key    string // normalized qualifier, empty for free text
	values []string
}

// parseFilter tokenizes a GitHub Projects view filter.
func parseFilter(filter string) []filterTerm {
	var terms []filterTerm
	for _, tok := range tokenize(filter) {
		t := filterTerm{}
		if strings.HasPrefix(tok, "-") && len(tok) > 1 {
			t.negate = true
			tok = tok[1:]
		}
		key, rest, found := cutUnquoted(tok, ':')
		if !found {
			t.values = []string{unquote(tok)}
			terms = append(terms, t)
			continue
		}
		t.key = normalizeName(unquote(key))
		for _, v := range splitUnquoted(rest, ',') {
			v = unquote(v)
			if v != "" {
				t.values = append(t.values, v)
			}
		}
		terms = append(terms, t)
	}
	return terms
}

func tokenize(s string) []string {
	var toks []string
	var cur strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && !inQuote:
			if cur.Len() > 0 {
				toks = append(toks, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		toks = append(toks, cur.String())
	}
	return toks
}

func cutUnquoted(s string, sep rune) (string, string, bool) {
	inQuote := false
	for i, r := range s {
		if r == '"' {
			inQuote = !inQuote
		} else if r == sep && !inQuote {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func splitUnquoted(s string, sep rune) []string {
	var parts []string
	for {
		before, after, found := cutUnquoted(s, sep)
		parts = append(parts, before)
		if !found {
			return parts
		}
		s = after
	}
}

func unquote(s string) string {
	return strings.Trim(s, `"`)
}

// normalizeName makes "Task Type", "task-type" and "TASK_TYPE" comparable.
func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer(" ", "-", "_", "-").Replace(s)
}

type filterContext struct {
	me     string
	fields map[string]data.ProjectField // keyed by normalized name
}

func newFilterContext(project *data.ProjectData, me string) filterContext {
	fc := filterContext{me: me, fields: map[string]data.ProjectField{}}
	if project != nil {
		for _, f := range project.Fields {
			fc.fields[normalizeName(f.Name)] = f
		}
	}
	return fc
}

func (fc filterContext) matchesAll(item *data.ProjectItem, terms []filterTerm) bool {
	for _, t := range terms {
		if fc.matches(item, t) == t.negate {
			return false
		}
	}
	return true
}

// matches reports whether the item satisfies the term, ignoring negation.
func (fc filterContext) matches(item *data.ProjectItem, t filterTerm) bool {
	c := &item.Content
	switch t.key {
	case "":
		return anyValue(t.values, func(v string) bool {
			return strings.Contains(strings.ToLower(c.Title), strings.ToLower(v)) ||
				(c.Number > 0 && strings.TrimPrefix(v, "#") == strconv.Itoa(c.Number))
		})
	case "is":
		return anyValue(t.values, func(v string) bool {
			switch strings.ToLower(v) {
			case "open":
				return c.State == "OPEN"
			case "closed":
				return c.State == "CLOSED" || c.State == "MERGED"
			case "merged":
				return c.State == "MERGED"
			case "issue":
				return item.ContentType == "Issue"
			case "pr":
				return item.ContentType == "PullRequest"
			case "draft":
				return item.ContentType == "DraftIssue"
			}
			return false
		})
	case "no", "has":
		want := t.key == "has"
		return anyValue(t.values, func(v string) bool {
			return fc.hasValue(item, normalizeName(v)) == want
		})
	case "assignee", "assignees":
		return anyValue(t.values, func(v string) bool {
			login := strings.TrimPrefix(v, "@")
			if v == "@me" {
				login = fc.me
			}
			for _, a := range c.Assignees.Nodes {
				if strings.EqualFold(a.Login, login) {
					return true
				}
			}
			return false
		})
	case "author":
		return anyValue(t.values, func(v string) bool {
			if v == "@me" {
				v = fc.me
			}
			return strings.EqualFold(c.Author.Login, strings.TrimPrefix(v, "@"))
		})
	case "label", "labels":
		return anyValue(t.values, func(v string) bool {
			for _, l := range c.Labels.Nodes {
				if strings.EqualFold(l.Name, v) {
					return true
				}
			}
			return false
		})
	case "repo", "repository":
		return anyValue(t.values, func(v string) bool {
			return strings.EqualFold(c.Repository.NameWithOwner, v) ||
				strings.EqualFold(c.Repository.Name, v)
		})
	case "title":
		return anyValue(t.values, func(v string) bool {
			return strings.Contains(strings.ToLower(c.Title), strings.ToLower(v))
		})
	}

	field, ok := fc.fields[t.key]
	if !ok {
		// Unknown qualifier: GitHub ignores it, so do we.
		return !t.negate
	}
	val, has := item.FieldValues[field.Name]
	return anyValue(t.values, func(v string) bool {
		if !has {
			return false
		}
		if val.Number != nil {
			return matchNumber(*val.Number, v)
		}
		return strings.EqualFold(val.String(), v)
	})
}

func (fc filterContext) hasValue(item *data.ProjectItem, key string) bool {
	c := &item.Content
	switch key {
	case "assignee", "assignees":
		return len(c.Assignees.Nodes) > 0
	case "label", "labels":
		return len(c.Labels.Nodes) > 0
	}
	if f, ok := fc.fields[key]; ok {
		_, has := item.FieldValues[f.Name]
		return has
	}
	return false
}

func anyValue(values []string, pred func(string) bool) bool {
	for _, v := range values {
		if pred(v) {
			return true
		}
	}
	return false
}

// matchNumber supports `5`, `>5`, `>=5`, `<5`, `<=5` and `1..5`.
func matchNumber(n float64, expr string) bool {
	parse := func(s string) (float64, bool) {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return f, err == nil
	}
	if lo, hi, ok := strings.Cut(expr, ".."); ok {
		l, ok1 := parse(lo)
		h, ok2 := parse(hi)
		return ok1 && ok2 && n >= l && n <= h
	}
	for _, op := range []string{">=", "<=", ">", "<"} {
		if rest, ok := strings.CutPrefix(expr, op); ok {
			v, ok := parse(rest)
			if !ok {
				return false
			}
			switch op {
			case ">=":
				return n >= v
			case "<=":
				return n <= v
			case ">":
				return n > v
			default:
				return n < v
			}
		}
	}
	v, ok := parse(expr)
	return ok && n == v
}
