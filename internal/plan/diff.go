package plan

import (
	"tide/internal/i18n"

	"encoding/json"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Diff limits keep a release row small; the full diff is always in Argo CD.
const (
	maxDiffBytes  = 16 << 10
	maxDiffLines  = 3000
	diffContext   = 3
	lastAppliedKy = "kubectl.kubernetes.io/last-applied-configuration"
)

// manifestYAML renders an Argo CD state document as YAML without the fields
// the cluster owns, so a diff shows only what git changes. "null" and ""
// render as nothing.
func manifestYAML(state string) (string, error) {
	state = strings.TrimSpace(state)
	if state == "" || state == "null" {
		return "", nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(state), &obj); err != nil {
		return "", fmt.Errorf("decode manifest: %w", err)
	}
	delete(obj, "status")
	if md, ok := obj["metadata"].(map[string]any); ok {
		for _, k := range []string{"managedFields", "resourceVersion", "uid", "generation", "creationTimestamp", "selfLink"} {
			delete(md, k)
		}
		if ann, ok := md["annotations"].(map[string]any); ok {
			delete(ann, lastAppliedKy)
			if len(ann) == 0 {
				delete(md, "annotations")
			}
		}
	}
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(obj); err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}
	return buf.String(), nil
}

// unifiedDiff returns a unified diff of two texts with diffContext lines of
// context, and whether it was truncated. Equal texts give "".
func unifiedDiff(before, after string) (string, bool) {
	if before == after {
		return "", false
	}
	a, b := splitLines(before), splitLines(after)
	if len(a)+len(b) > maxDiffLines {
		return i18n.T(i18n.Default, "pl.diffTooBig", len(a), len(b)), true
	}
	ops := diffOps(a, b)
	var out strings.Builder
	truncated := false
	for start := 0; start < len(ops); {
		// Find the next change and the hunk around it.
		i := start
		for i < len(ops) && ops[i].kind == ' ' {
			i++
		}
		if i == len(ops) {
			break
		}
		lo := max(i-diffContext, start)
		hi := i
		for hi < len(ops) {
			if ops[hi].kind != ' ' {
				hi++
				continue
			}
			run := hi
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-hi > 2*diffContext {
				hi = min(hi+diffContext, len(ops))
				break
			}
			hi = run
		}
		fmt.Fprintf(&out, "@@ -%d +%d @@\n", ops[lo].a+1, ops[lo].b+1)
		for _, op := range ops[lo:hi] {
			out.WriteByte(op.kind)
			out.WriteString(op.text)
			out.WriteByte('\n')
		}
		if out.Len() > maxDiffBytes {
			truncated = true
			break
		}
		start = hi
	}
	s := out.String()
	if len(s) > maxDiffBytes {
		cut := strings.LastIndexByte(s[:maxDiffBytes], '\n')
		s, truncated = s[:cut+1], true
	}
	return s, truncated
}

type diffOp struct {
	kind byte // ' ', '-', '+'
	text string
	a, b int // line index in before / after where this op sits
}

// diffOps is a longest-common-subsequence line diff; manifests are small
// enough (capped by maxDiffLines) for the quadratic table.
func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i], i, j})
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, diffOp{'-', a[i], i, j})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j], i, j})
			j++
		}
	}
	return ops
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
