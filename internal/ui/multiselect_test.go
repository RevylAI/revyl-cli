package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMultiSelectStartsEmptyAndTogglesOnlyChosenOption(t *testing.T) {
	model := multiSelectModel{
		options:  []SelectOption{{Label: "First", Value: "first"}, {Label: "Second", Value: "second"}},
		selected: map[string]bool{},
	}
	if strings.Contains(model.View(), "[x]") {
		t.Fatal("unrequested selections are checked")
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeySpace})
	result := updated.(multiSelectModel)
	if result.selected["first"] || !result.selected["second"] {
		t.Fatalf("selection = %v", result.selected)
	}
	updated, _ = result.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !updated.(multiSelectModel).done {
		t.Fatal("enter did not finish selection")
	}
}

func TestMultiSelectEmptySelectionAndCancellationAreDistinct(t *testing.T) {
	model := multiSelectModel{options: []SelectOption{{Value: "first"}}, selected: map[string]bool{}}
	finished, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if finished.(multiSelectModel).cancelled || len(finished.(multiSelectModel).selected) != 0 {
		t.Fatal("empty selection must remain an intentional skip")
	}
	cancelled, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !cancelled.(multiSelectModel).cancelled {
		t.Fatal("escape did not cancel")
	}
}

func TestMultiSelectAllRequiresExplicitKey(t *testing.T) {
	model := multiSelectModel{options: []SelectOption{{Value: "first"}, {Value: "second"}}, selected: map[string]bool{}}
	all, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !all.(multiSelectModel).selected["first"] || !all.(multiSelectModel).selected["second"] {
		t.Fatal("explicit select all did not select both options")
	}
	none, _ := all.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if none.(multiSelectModel).selected["first"] || none.(multiSelectModel).selected["second"] {
		t.Fatal("toggle all did not clear selections")
	}
}
