package plugin

import (
	"strings"
	"testing"
)

// NewConsentFacts is the installer's half of the consent screen: it turns a
// manifest and a source it has just resolved into the facts core hashes and
// diffs. These tests run the whole way through — manifest to facts to digest to
// diff — because the seam between the two packages is exactly where a fact could
// go missing without either half looking wrong on its own.
//
// viewManifest and notebookManifest are in fragment_test.go, the other file that
// reads them.

// The consent screen reports the declaration, and the approval binds it: an
// update that stops offering `compact` to the drawer changes what the user sees
// in their drawer, and must not pass as "nothing you approved has changed".
func TestConsentReportsAndBindsTheViewDeclaration(t *testing.T) {
	m, err := ParseManifest([]byte(viewManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	facts := NewConsentFacts(m, ResolvedSource{Commit: "abc"}, []byte(viewManifest), "/opt/grove/bin/breaktimer")
	if len(facts.Views) != 2 {
		t.Fatalf("consent views = %v, want 2", facts.Views)
	}
	if !strings.HasPrefix(facts.Views[0], "full — clock, history and help") {
		t.Errorf("first line = %q", facts.Views[0])
	}
	// The line says which one a drawer would get, because that is the fact the
	// bool decides and the user cannot infer it from the names.
	if !strings.Contains(facts.Views[1], "by default") {
		t.Errorf("the drawer default is not marked: %q", facts.Views[1])
	}

	// Flipping the bool is a change to the approval, and Diff names it.
	flipped := strings.Replace(viewManifest, "drawer      = true", "drawer      = false", 1)
	m2, err := ParseManifest([]byte(flipped))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	next := NewConsentFacts(m2, ResolvedSource{Commit: "abc"}, []byte(flipped), "/opt/grove/bin/breaktimer")
	if facts.Digest() == next.Digest() {
		t.Error("withdrawing a view from the drawer did not change the approval digest")
	}
	var named bool
	for _, c := range Diff(facts, next) {
		if c.Field == "views" {
			named = true
		}
	}
	if !named {
		t.Errorf("Diff did not report the view change: %+v", Diff(facts, next))
	}
}

// The consent screen reports the declaration and the approval binds it: an
// update that moves the subtree changes what appears in the user's notebook,
// and must not pass as "nothing you approved has changed".
func TestConsentReportsAndBindsTheNotebookDeclaration(t *testing.T) {
	m, err := ParseManifest([]byte(notebookManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	facts := NewConsentFacts(m, ResolvedSource{Commit: "abc"}, []byte(notebookManifest), "/opt/grove/bin/grove-panel-hn")
	if facts.NotebookSubtree != "hn/clippings" || facts.NotebookDescription != "stories you clip from the feed" {
		t.Errorf("consent notebook = %q / %q", facts.NotebookSubtree, facts.NotebookDescription)
	}

	moved := strings.Replace(notebookManifest, `"hn/clippings"`, `"hn/archive"`, 1)
	m2, err := ParseManifest([]byte(moved))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	next := NewConsentFacts(m2, ResolvedSource{Commit: "abc"}, []byte(moved), "/opt/grove/bin/grove-panel-hn")
	if facts.Digest() == next.Digest() {
		t.Error("moving the notebook subtree did not change the approval digest")
	}
	var named bool
	for _, c := range Diff(facts, next) {
		if c.Field == "notebook" {
			named = true
			if !strings.Contains(c.Old, "hn/clippings") || !strings.Contains(c.New, "hn/archive") {
				t.Errorf("notebook diff = %+v, want both subtrees named", c)
			}
		}
	}
	if !named {
		t.Errorf("Diff did not report the notebook change: %+v", Diff(facts, next))
	}

	// Withdrawing the declaration entirely is also a change: the panel stops
	// saying what it does with the notebook, which the user should see.
	if changes := Diff(facts, ConsentFacts{}); len(changes) == 0 {
		t.Error("removing the notebook declaration diffed as nothing")
	}
}
