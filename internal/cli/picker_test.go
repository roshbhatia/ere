package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPickerFiltersAndSelectsWithSharedKeys(t *testing.T) {
	m := picker{choices: []choice{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}}, width: 20, height: 8}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("/")}, {Type: tea.KeyRunes, Runes: []rune("bet")}, {Type: tea.KeyEnter}, {Type: tea.KeyEnter}} {
		next, _ := m.Update(key)
		m = next.(picker)
	}
	if m.selected != "b" {
		t.Fatalf("selected %q", m.selected)
	}
}

func TestPickerEmptyFilterAndTinyViewport(t *testing.T) {
	m := picker{choices: []choice{{ID: "a", Label: "Alpha"}}, query: "missing", width: 1, height: 1}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(picker).selected != "" {
		t.Fatal("selected absent item")
	}
	if !strings.Contains(m.View(), "No matches") {
		t.Fatal(m.View())
	}
}
