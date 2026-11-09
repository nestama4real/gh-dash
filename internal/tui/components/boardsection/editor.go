package boardsection

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
)

type editorStage int

const (
	stageFields editorStage = iota
	stageOptions
	stageInput
)

const editorWidth = 46

type editorOption struct {
	id    string
	name  string
	color string
	clear bool
}

type fieldEditor struct {
	itemId  string
	label   string
	stage   editorStage
	fields  []data.ProjectField
	cursor  int
	field   data.ProjectField
	options []editorOption
	optCur  int
	input   textinput.Model
	err     string
	// direct is set when the editor was opened on one field (t / T):
	// esc then closes it instead of going back to the field list.
	direct bool
}

// OpenEditor opens the field editor on the selected card.
func (m *Model) OpenEditor() tea.Cmd {
	m.rebuild()
	item := m.currItem()
	if item == nil || m.store.Project == nil {
		return nil
	}
	var fields []data.ProjectField
	statusName := m.Ctx.Config.ScrumBoard.GetStatusField()
	if f, ok := m.store.Project.Field(statusName); ok {
		fields = append(fields, f)
	}
	for _, f := range m.store.Project.Fields {
		if f.IsEditable() && !strings.EqualFold(f.Name, statusName) {
			fields = append(fields, f)
		}
	}
	label := "Draft"
	if item.Content.Number > 0 {
		label = fmt.Sprintf("#%d", item.Content.Number)
	}
	m.editor = &fieldEditor{itemId: item.Id, label: label, fields: fields}
	return nil
}

// OpenFieldEditor opens the editor straight on the first field matching
// match, e.g. the estimate. It does nothing when no field matches.
func (m *Model) OpenFieldEditor(match func(fieldName string) bool) tea.Cmd {
	m.OpenEditor()
	e := m.editor
	if e == nil {
		return nil
	}
	for i, f := range e.fields {
		if match(f.Name) {
			e.cursor = i
			e.direct = true
			return e.openField(m, m.findItem(e.itemId))
		}
	}
	m.editor = nil
	return nil
}

func IsEstimateField(name string) bool { return isEstimateField(name) }

func IsActualTimeField(name string) bool { return isActualField(name) }

func (e *fieldEditor) openField(m *Model, item *data.ProjectItem) tea.Cmd {
	f := e.fields[e.cursor]
	e.field = f
	e.err = ""
	current, has := item.FieldValues[f.Name]
	switch f.DataType {
	case "SINGLE_SELECT", "ITERATION":
		e.options = nil
		if f.DataType == "SINGLE_SELECT" {
			for _, o := range f.Options {
				e.options = append(e.options, editorOption{id: o.Id, name: o.Name, color: o.Color})
			}
		} else {
			for _, it := range f.Iterations {
				e.options = append(e.options, editorOption{id: it.Id, name: it.Title, color: "GRAY"})
			}
		}
		e.options = append(e.options, editorOption{name: "Clear", clear: true})
		e.optCur = 0
		for i, o := range e.options {
			if has && o.id == current.OptionId {
				e.optCur = i
			}
		}
		e.stage = stageOptions
		return nil
	default:
		ti := textinput.New()
		ti.Prompt = "› "
		switch f.DataType {
		case "NUMBER":
			ti.Placeholder = "number, empty to clear"
		case "DATE":
			ti.Placeholder = "YYYY-MM-DD, empty to clear"
		default:
			ti.Placeholder = "text, empty to clear"
		}
		if has {
			ti.SetValue(current.String())
		}
		ti.SetWidth(editorWidth - 8)
		ti.CursorEnd()
		e.input = ti
		e.stage = stageInput
		return e.input.Focus()
	}
}

func (m *Model) updateEditor(msg tea.KeyPressMsg) tea.Cmd {
	e := m.editor
	item := m.findItem(e.itemId)
	if item == nil {
		m.editor = nil
		return nil
	}
	key := msg.String()

	switch e.stage {
	case stageFields:
		switch key {
		case "esc", "q", "ctrl+c":
			m.editor = nil
		case "up", "k", "shift+tab":
			e.cursor = (e.cursor - 1 + len(e.fields)) % len(e.fields)
		case "down", "j", "tab":
			e.cursor = (e.cursor + 1) % len(e.fields)
		case "enter", "l", "right", "space":
			if len(e.fields) > 0 {
				return e.openField(m, item)
			}
		}
		return nil

	case stageOptions:
		switch key {
		case "esc", "h", "left":
			if e.direct {
				m.editor = nil
				return nil
			}
			e.stage = stageFields
		case "ctrl+c", "q":
			m.editor = nil
		case "up", "k", "shift+tab":
			e.optCur = (e.optCur - 1 + len(e.options)) % len(e.options)
		case "down", "j", "tab":
			e.optCur = (e.optCur + 1) % len(e.options)
		case "enter", "space":
			o := e.options[e.optCur]
			m.editor = nil
			if o.clear {
				return m.applyEdit(item, e.field, nil, nil)
			}
			inputKey := "singleSelectOptionId"
			if e.field.DataType == "ITERATION" {
				inputKey = "iterationId"
			}
			return m.applyEdit(item, e.field,
				&data.ProjectFieldValue{OptionId: o.id, Name: o.name},
				map[string]any{inputKey: o.id})
		}
		return nil

	case stageInput:
		switch key {
		case "esc":
			if e.direct {
				m.editor = nil
				return nil
			}
			e.stage = stageFields
			e.input.Blur()
			return nil
		case "ctrl+c":
			m.editor = nil
			return nil
		case "enter":
			raw := strings.TrimSpace(e.input.Value())
			if raw == "" {
				m.editor = nil
				return m.applyEdit(item, e.field, nil, nil)
			}
			value, input, err := parseFieldInput(e.field, raw)
			if err != nil {
				e.err = err.Error()
				return nil
			}
			m.editor = nil
			return m.applyEdit(item, e.field, value, input)
		}
		var cmd tea.Cmd
		e.input, cmd = e.input.Update(msg)
		e.err = ""
		return cmd
	}
	return nil
}

func parseFieldInput(f data.ProjectField, raw string) (*data.ProjectFieldValue, map[string]any, error) {
	switch f.DataType {
	case "NUMBER":
		n, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", "."), 64)
		if err != nil {
			return nil, nil, fmt.Errorf("%q is not a number", raw)
		}
		return &data.ProjectFieldValue{Number: &n}, map[string]any{"number": n}, nil
	case "DATE":
		if _, err := time.Parse("2006-01-02", raw); err != nil {
			return nil, nil, fmt.Errorf("expected a date like 2026-11-09")
		}
		return &data.ProjectFieldValue{Date: raw}, map[string]any{"date": raw}, nil
	default:
		return &data.ProjectFieldValue{Text: raw}, map[string]any{"text": raw}, nil
	}
}

func (m *Model) applyEdit(item *data.ProjectItem, f data.ProjectField,
	value *data.ProjectFieldValue, input map[string]any,
) tea.Cmd {
	if _, has := item.FieldValues[f.Name]; !has && value == nil {
		return nil
	}
	if strings.EqualFold(f.Name, m.Ctx.Config.ScrumBoard.GetStatusField()) {
		m.follow = item.Id
	}
	cmd := m.setField(item, f, value, input)
	m.rebuild()
	return cmd
}

func (m *Model) renderEditor() string {
	e := m.editor
	item := m.findItem(e.itemId)
	if item == nil {
		return ""
	}
	theme := m.Ctx.Theme
	faint := lipgloss.NewStyle().Foreground(theme.FaintText)
	inner := editorWidth - 4

	title := lipgloss.NewStyle().Bold(true).Foreground(theme.PrimaryText).
		Render(ansi.Truncate(fmt.Sprintf("Edit %s %s", e.label, item.Content.Title), inner, "…"))
	lines := []string{title, ""}

	valueStyle := func(f data.ProjectField, v data.ProjectFieldValue) string {
		if f.DataType == "SINGLE_SELECT" {
			if o, ok := f.OptionByName(v.Name); ok {
				return lipgloss.NewStyle().Foreground(m.ghColor(o.Color)).Bold(true).Render(v.Name)
			}
		}
		return lipgloss.NewStyle().Foreground(theme.PrimaryText).Render(v.String())
	}

	var hint string
	switch e.stage {
	case stageFields:
		for i, f := range e.fields {
			cursor := "  "
			name := lipgloss.NewStyle().Foreground(theme.SecondaryText).Render(f.Name)
			if i == e.cursor {
				cursor = lipgloss.NewStyle().Foreground(theme.PrimaryText).Render("› ")
				name = lipgloss.NewStyle().Foreground(theme.PrimaryText).Bold(true).Render(f.Name)
			}
			val := faint.Render("—")
			if v, ok := item.FieldValues[f.Name]; ok {
				val = valueStyle(f, v)
			}
			pad := max(1, 18-ansi.StringWidth(f.Name))
			lines = append(lines, ansi.Truncate(cursor+name+strings.Repeat(" ", pad)+val, inner, "…"))
		}
		hint = "j/k move • enter edit • esc close"
	case stageOptions:
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.SecondaryText).
			Render(e.field.Name+":"), "")
		for i, o := range e.options {
			cursor := "  "
			if i == e.optCur {
				cursor = lipgloss.NewStyle().Foreground(theme.PrimaryText).Render("› ")
			}
			var label string
			if o.clear {
				label = faint.Render("✕ Clear")
			} else {
				st := lipgloss.NewStyle().Foreground(m.ghColor(o.color))
				if i == e.optCur {
					st = st.Bold(true)
				}
				label = st.Render("● " + o.name)
			}
			lines = append(lines, ansi.Truncate(cursor+label, inner, "…"))
		}
		hint = "j/k move • enter apply • esc back"
	case stageInput:
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.SecondaryText).
			Render(e.field.Name+":"), "", e.input.View())
		if e.err != "" {
			lines = append(lines, "", lipgloss.NewStyle().Foreground(theme.ErrorText).Render(e.err))
		}
		hint = "enter apply • esc back"
		if e.direct {
			hint = "enter apply • esc cancel"
		}
	}
	lines = append(lines, "", faint.Render(hint))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.PrimaryText).
		Padding(0, 1).
		Width(editorWidth).
		Render(strings.Join(lines, "\n"))
}

func (m *Model) overlayEditor(board string, width, height int) string {
	popup := m.renderEditor()
	if popup == "" {
		return board
	}
	x := max(0, (width-lipgloss.Width(popup))/2)
	y := max(0, (height-lipgloss.Height(popup))/3)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(board),
		lipgloss.NewLayer(popup).X(x).Y(y).Z(1),
	).Render()
}
