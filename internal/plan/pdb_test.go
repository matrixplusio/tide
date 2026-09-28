package plan

import "testing"

// A budget that would pin the last Pod is refused; anything that still leaves
// one Pod evictable is not, and a count of zero is never a drain problem.
func TestPDBBlocks(t *testing.T) {
	pdb := func(field string, v any) map[string]any {
		return map[string]any{"spec": map[string]any{field: v}}
	}
	cases := map[string]struct {
		pdb  map[string]any
		to   int
		want bool
	}{
		"minAvailable 1, to 1: the only Pod can never go": {pdb("minAvailable", float64(1)), 1, true},
		"minAvailable 1, to 2: one can go":                {pdb("minAvailable", float64(1)), 2, false},
		"minAvailable 2, to 2":                            {pdb("minAvailable", float64(2)), 2, true},
		"minAvailable 50%, to 1 rounds up to 1":           {pdb("minAvailable", "50%"), 1, true},
		"minAvailable 50%, to 2 leaves one":               {pdb("minAvailable", "50%"), 2, false},
		"minAvailable 100%":                               {pdb("minAvailable", "100%"), 5, true},
		"maxUnavailable 0":                                {pdb("maxUnavailable", float64(0)), 3, true},
		"maxUnavailable 1":                                {pdb("maxUnavailable", float64(1)), 1, false},
		"maxUnavailable 10%, to 3 rounds up to 1":         {pdb("maxUnavailable", "10%"), 3, false},
		"neither field: allows nothing":                   {map[string]any{"spec": map[string]any{}}, 2, true},
		"to 0 never blocks":                               {pdb("minAvailable", float64(1)), 0, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := PDBBlocks(c.pdb, c.to); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
