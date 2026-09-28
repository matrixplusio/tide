// Package manifest makes one small, exact edit to a Kubernetes manifest that
// people maintain in git.
//
// Editing is surgical, on the YAML node tree, not a decode into maps and a
// re-encode. These files are written and read by people: they carry comments
// that say why a value is what it is, and a re-encode would drop them and
// reorder every key, turning a one-field change into a diff nobody can review.
package manifest

import (
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Doc is a parsed manifest that remembers its formatting.
type Doc struct {
	node yaml.Node
}

// Parse reads one manifest. A file holding several documents is refused
// rather than half-understood: the replica count would be set on whichever
// document happened to come first.
func Parse(text string) (*Doc, error) {
	var d Doc
	if err := yaml.Unmarshal([]byte(text), &d.node); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if len(d.node.Content) == 0 {
		return nil, fmt.Errorf("manifest is empty")
	}
	if d.node.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("manifest is not a single object")
	}
	if strings.Count(text, "\n---") > 0 {
		return nil, fmt.Errorf("manifest holds more than one document; Tide will not guess which one to scale")
	}
	return &d, nil
}

// Kind and Name say what the file turned out to hold, so a caller can check it
// is the workload it meant to edit rather than trusting the path.
func (d *Doc) Kind() string { return field(d.node.Content[0], "kind") }

func (d *Doc) Name() string {
	md := child(d.node.Content[0], "metadata")
	if md == nil {
		return ""
	}
	return field(md, "name")
}

// Replicas reads the current count. Absent means 1, as it does to Kubernetes —
// reading it as 0 would make an untouched manifest look like a stopped one.
func (d *Doc) Replicas() (int, bool) {
	spec := child(d.node.Content[0], "spec")
	if spec == nil {
		return 1, false
	}
	v := field(spec, "replicas")
	if v == "" {
		return 1, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 1, false
	}
	return n, true
}

// SetReplicas writes the count and reports what was there. It refuses to add
// the field: a workload whose manifest never said how many it wants is one
// whose count comes from somewhere else, and writing one here would start a
// fight nobody is watching.
func (d *Doc) SetReplicas(n int) (int, error) {
	spec := child(d.node.Content[0], "spec")
	if spec == nil {
		return 0, fmt.Errorf("manifest has no spec")
	}
	for i := 0; i+1 < len(spec.Content); i += 2 {
		if spec.Content[i].Value != "replicas" {
			continue
		}
		was, err := strconv.Atoi(spec.Content[i+1].Value)
		if err != nil {
			return 0, fmt.Errorf("replicas is %q, which is not a number", spec.Content[i+1].Value)
		}
		spec.Content[i+1].Value = strconv.Itoa(n)
		spec.Content[i+1].Tag = "!!int"
		return was, nil
	}
	return 0, fmt.Errorf("manifest has no replicas field, so the count is set somewhere else")
}

// String renders the file back.
func (d *Doc) String() (string, error) {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&d.node); err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}
	return buf.String(), nil
}

func child(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func field(m *yaml.Node, key string) string {
	if v := child(m, key); v != nil {
		return v.Value
	}
	return ""
}
