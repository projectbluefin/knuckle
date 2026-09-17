package model

import "testing"

// The OS roster is the single source of truth for which OS targets exist and in
// what order they are offered. These tests pin the properties every consumer
// relies on, so a future edit that breaks one of them fails here rather than in
// the installer.

func TestOSTargetIDsAreInRosterOrder(t *testing.T) {
	targets := OSTargets()
	ids := OSTargetIDs()

	if len(ids) != len(targets) {
		t.Fatalf("OSTargetIDs returned %d ids for %d targets", len(ids), len(targets))
	}
	for i := range targets {
		if ids[i] != targets[i].ID {
			t.Errorf("index %d: OSTargetIDs = %q, OSTargets = %q", i, ids[i], targets[i].ID)
		}
	}
}

// TestEveryOSConstantIsRegistered fails when an OS discriminator constant is
// declared but never added to the roster. An unregistered constant is a target
// the OS picker can never offer.
func TestEveryOSConstantIsRegistered(t *testing.T) {
	for _, id := range []string{OSFlatcar, OSFCOS, OSBluefinDDI} {
		if !IsKnownOS(id) {
			t.Errorf("OS constant %q is not in the OSTargets roster", id)
		}
	}
}

func TestRosterEntriesAreCompleteAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i, target := range OSTargets() {
		if target.ID == "" {
			t.Errorf("roster entry %d has an empty ID", i)
		}
		if target.Name == "" {
			t.Errorf("roster entry %d (%q) has an empty Name", i, target.ID)
		}
		if target.Description == "" {
			t.Errorf("roster entry %d (%q) has an empty Description", i, target.ID)
		}
		if seen[target.ID] {
			t.Errorf("roster entry %d repeats ID %q", i, target.ID)
		}
		seen[target.ID] = true
	}
}

func TestIsKnownOSRejectsUnregisteredValues(t *testing.T) {
	for _, id := range []string{"", "Flatcar", "rhcos", "bluefin", "fcos "} {
		if IsKnownOS(id) {
			t.Errorf("IsKnownOS(%q) = true, want false", id)
		}
	}
}

// TestOSTargetsIsDefensivelyCopied guards the roster against callers that
// mutate the slice they are handed — the failure mode a package-level exported
// var would have.
func TestOSTargetsIsDefensivelyCopied(t *testing.T) {
	first := OSTargets()
	if len(first) == 0 {
		t.Fatal("roster is empty")
	}
	original := first[0]
	first[0] = OSTarget{ID: "tampered", Name: "x", Description: "x"}

	if again := OSTargets(); again[0] != original {
		t.Errorf("mutating the returned slice changed the roster: got %+v, want %+v", again[0], original)
	}
	if ids := OSTargetIDs(); ids[0] != original.ID {
		t.Errorf("OSTargetIDs reflects caller mutation: got %q, want %q", ids[0], original.ID)
	}

	ids := OSTargetIDs()
	ids[0] = "tampered"
	if again := OSTargetIDs(); again[0] != original.ID {
		t.Errorf("mutating returned ids changed the roster: got %q, want %q", again[0], original.ID)
	}
}
