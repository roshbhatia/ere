package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/roshbhatia/go-utils/cell"
	"github.com/roshbhatia/go-utils/keymap"
	"github.com/roshbhatia/go-utils/store"
	"github.com/roshbhatia/go-utils/terminal"
	"github.com/roshbhatia/go-utils/ui"
	"github.com/spf13/cobra"
)

type (
	choice struct{ ID, Label string }
	picker struct {
		title                 string
		choices               []choice
		query                 string
		cursor, width, height int
		selected              string
		filtering             bool
	}
)

var pickerKeys = keymap.Must(
	keymap.Binding{ID: "open", Keys: []string{"enter"}, Short: "select", Description: "select item"},
	keymap.Binding{ID: "filter", Keys: []string{"/"}, Short: "filter", Description: "filter items"},
	keymap.Binding{ID: "cancel", Keys: []string{"esc", "ctrl+c"}, Short: "cancel", Description: "cancel selection"},
)

func (m picker) Init() tea.Cmd { return nil }
func (m picker) matches() []choice {
	var result []choice
	for _, c := range m.choices {
		if strings.Contains(strings.ToLower(c.Label), strings.ToLower(m.query)) {
			result = append(result, c)
		}
	}
	return result
}

func (m picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = k.Width
		m.height = k.Height
	case tea.KeyMsg:
		if k.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.filtering {
			switch k.Type {
			case tea.KeyEsc:
				m.filtering = false
			case tea.KeyEnter:
				m.filtering = false
			case tea.KeyBackspace, tea.KeyDelete:
				r := []rune(m.query)
				if len(r) > 0 {
					m.query = string(r[:len(r)-1])
				}
			case tea.KeyRunes:
				m.query += store.OneLine(string(k.Runes))
			}
			m.cursor = 0
			return m, nil
		}
		if k.String() == "/" {
			m.filtering = true
			return m, nil
		}
		action, _ := ui.ActionFor(ui.Key{Name: k.String()})
		switch action {
		case ui.ActionUp:
			m.cursor = max(0, m.cursor-1)
		case ui.ActionDown:
			m.cursor = min(max(0, len(m.matches())-1), m.cursor+1)
		case ui.ActionOpen:
			items := m.matches()
			if len(items) > 0 {
				m.selected = items[m.cursor].ID
				return m, tea.Quit
			}
		case ui.ActionBack, ui.ActionQuit:
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m picker) View() string {
	if m.selected != "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(store.OneLine(m.title) + "\n")
	if m.filtering || m.query != "" {
		b.WriteString("/ " + m.query + "\n")
	}
	rows := max(1, m.height-5)
	start := max(0, m.cursor-rows+1)
	items := m.matches()
	for i := start; i < min(len(items), start+rows); i++ {
		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}
		b.WriteString(prefix + cell.Truncate(store.OneLine(items[i].Label), max(1, m.width-3)) + "\n")
	}
	if len(items) == 0 {
		b.WriteString("No matches\n")
	}
	hint, _ := pickerKeys.HintLine("  ", "open", "filter", "cancel")
	b.WriteString("j/k move  " + hint + "\n")
	return b.String()
}

func pick(cmd *cobra.Command, title string, items []choice) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("no %s available", strings.ToLower(title))
	}
	if !terminal.IsTTY(cmd.InOrStdin()) || !terminal.IsTTY(cmd.OutOrStdout()) {
		return "", fmt.Errorf("specify an item explicitly when input or output is not a terminal")
	}
	model, err := tea.NewProgram(picker{title: title, choices: items, width: 80, height: 18}, tea.WithContext(cmd.Context()), tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout())).Run()
	if err != nil {
		return "", err
	}
	result, ok := model.(picker)
	if !ok || result.selected == "" {
		return "", fmt.Errorf("selection cancelled")
	}
	return result.selected, nil
}
