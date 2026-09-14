package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

var (
	ansiRE   = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	boxRE    = regexp.MustCompile(`[\x{2500}-\x{257F}\x{25B8}]`)
	spacesRE = regexp.MustCompile(`\s+`)
)

// flatten strips the ANSI styling, box-drawing characters and soft wrapping
// lipgloss adds, so rendered prose can be compared with the roster strings.
func flatten(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	s = boxRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(spacesRE.ReplaceAllString(s, " "))
}

// The OS picker is three surfaces indexed by one cursor: the cards rendered by
// viewChannelCards, the ID selected by handleEnter, and the cursor ceiling from
// maxCursor. They used to be three independent literal lists. These tests fail
// if any of them stops agreeing with model.OSTargets(), which is the drift that
// would otherwise let a user install a different OS than the card they had
// highlighted.

func newOSPickerModel(t *testing.T) *Model {
	t.Helper()
	w := newTestWizard()
	w.State.CurrentStep = model.StepWelcome
	m := New(w)
	if !m.osSubView {
		t.Fatal("osSubView should be true at StepWelcome")
	}
	return m
}

func TestOSPickerRendersEveryRosterEntryInOrder(t *testing.T) {
	m := newOSPickerModel(t)
	out := flatten(m.viewChannelCards())

	prev := -1
	for i, target := range model.OSTargets() {
		at := strings.Index(out, target.Name)
		if at < 0 {
			t.Errorf("OS picker does not render roster entry %d (%q)", i, target.Name)
			continue
		}
		if at <= prev {
			t.Errorf("roster entry %d (%q) is rendered out of roster order", i, target.Name)
		}
		prev = at

		if !strings.Contains(out, flatten(target.Description)) {
			t.Errorf("OS picker does not render the description for %q", target.ID)
		}
	}
}

func TestOSPickerCursorSelectsTheRosterEntryItRenders(t *testing.T) {
	for i, target := range model.OSTargets() {
		m := newOSPickerModel(t)
		m.cursor = i
		_, _ = m.handleEnter()

		if got := m.Wizard.State.Config.OS; got != target.ID {
			t.Errorf("cursor %d renders %q but selects OS %q, want %q",
				i, target.Name, got, target.ID)
		}
	}
}

// TestOSPickerCursorCeilingCoversTheWholeRoster catches the case where a target
// is added to the roster but maxCursor still stops short of it, leaving the
// last entry rendered but unreachable.
func TestOSPickerCursorCeilingCoversTheWholeRoster(t *testing.T) {
	m := newOSPickerModel(t)
	if got, want := m.maxCursor(), len(model.OSTargetIDs()); got != want {
		t.Errorf("maxCursor() = %d at the OS picker, want %d (roster size)", got, want)
	}
}

// TestOSPickerSelectionIsBoundedByTheRoster documents that an out-of-range
// cursor leaves the OS untouched rather than selecting an arbitrary target.
func TestOSPickerSelectionIsBoundedByTheRoster(t *testing.T) {
	m := newOSPickerModel(t)
	m.Wizard.State.Config.OS = model.OSFlatcar
	m.cursor = len(model.OSTargetIDs())
	_, _ = m.handleEnter()

	if got := m.Wizard.State.Config.OS; got != model.OSFlatcar {
		t.Errorf("cursor past the roster changed OS to %q, want it left at %q", got, model.OSFlatcar)
	}
}
