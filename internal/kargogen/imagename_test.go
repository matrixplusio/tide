package kargogen

import (
	"strings"
	"testing"
)

// kustomize-set-image matches an entry by its name, and the name is not the
// image. Against an overlay whose entry is called something else, a step that
// names only the repository matches nothing — and Kargo appends a second entry
// rather than failing. The two then work as a chain: the second selects what
// the first renamed. Which means the first entry's tag is frozen forever, and
// changing the first entry's newName turns the second into a dead selector,
// so every tag written afterwards silently does nothing.
func TestNamingTheImageEntryAlsoRenamesIt(t *testing.T) {
	task := taskYAML("acme-base", "promote-image", "PLACEHOLDER")
	for _, want := range []string{
		"name: PLACEHOLDER",
		"newName: ${{ vars.imageRepo }}",
		"tag: ${{ imageFrom(vars.imageRepo).Tag }}",
	} {
		if !strings.Contains(task, want) {
			t.Errorf("the promotion task is missing %q:\n%s", want, task)
		}
	}
	// newName without name, or name without newName, are both broken: the
	// first matches nothing, the second sets a tag on an entry whose registry
	// path has been dropped.
	if strings.Count(task, "newName:") != strings.Count(task, "name: PLACEHOLDER") {
		t.Errorf("name and newName have to come together:\n%s", task)
	}
}

// An overlay whose entry is already the full image name needs no rename, and
// naming it would be noise. Nothing is guessed: not configured, not emitted.
func TestWithoutAConfiguredEntryNameTheStepStaysPlain(t *testing.T) {
	task := taskYAML("acme-base", "promote-image", "")
	if strings.Contains(task, "newName:") {
		t.Errorf("a rename appeared with no entry name configured:\n%s", task)
	}
	if !strings.Contains(task, "- image: ${{ vars.imageRepo }}\n            tag:") {
		t.Errorf("the plain form changed shape:\n%s", task)
	}
}

// The task is YAML that Kargo parses. An indentation mistake in the inserted
// lines would produce a file that applies and then does nothing useful.
func TestTheInsertedLinesLineUpWithTheEntry(t *testing.T) {
	task := taskYAML("acme-base", "promote-image", "PLACEHOLDER")
	var image, name, newName, tag string
	for _, line := range strings.Split(task, "\n") {
		switch {
		case strings.Contains(line, "- image: ${{ vars.imageRepo }}"):
			image = line
		case strings.Contains(line, "name: PLACEHOLDER"):
			name = line
		case strings.Contains(line, "newName:"):
			newName = line
		case strings.Contains(line, "tag: ${{ imageFrom"):
			tag = line
		}
	}
	indent := func(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }
	// "- image:" starts the entry, so its keys sit two columns further in.
	want := indent(image) + 2
	for label, line := range map[string]string{"name": name, "newName": newName, "tag": tag} {
		if line == "" {
			t.Fatalf("%s is missing entirely:\n%s", label, task)
		}
		if got := indent(line); got != want {
			t.Errorf("%s is indented %d, the entry's other keys are at %d:\n%s", label, got, want, task)
		}
	}
}
