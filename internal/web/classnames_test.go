package web_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A component that writes className="stepi skip" also gets every rule for
// `.skip` — and `.skip` is the skip-to-content link, positioned absolutely
// with a z-index. The icon left its row, covered the tick above it, and the
// row it belonged to rendered nothing at all.
//
// Sharing a name with an ordinary utility is fine and common — "mono
// ellipsis" is two utilities doing two things. What is not fine is sharing
// one with a rule that takes the element out of the flow, because then the
// element goes somewhere else entirely. So the rule is narrow: a class used
// as a modifier must not, on its own, position anything.
//
// This lives in Go because Vitest hands back an empty string for a CSS
// import, so the same check written over there passes whatever the CSS says.
func TestNoModifierClassPositionsOnItsOwn(t *testing.T) {
	const root = "../../web/src"
	css, err := os.ReadFile(filepath.Join(root, "styles/base.css"))
	if err != nil {
		t.Skipf("frontend not present: %v", err)
	}
	if len(css) == 0 {
		t.Fatal("base.css is empty — this check would pass without checking anything")
	}

	// Rules of the form ".name { ... }" whose body takes the element out of
	// the normal flow.
	positioned := map[string]bool{}
	rule := regexp.MustCompile(`(?m)^\.([a-z][\w-]*)\s*(?:,[^{]*)?\{([^}]*)\}`)
	for _, m := range rule.FindAllSubmatch(css, -1) {
		if regexp.MustCompile(`position\s*:\s*(absolute|fixed)`).Match(m[2]) {
			positioned[string(m[1])] = true
		}
	}
	if !positioned["skip"] {
		t.Fatal("did not recognise .skip as positioned — the parser is not reading rules, so nothing below means anything")
	}

	classAttr := regexp.MustCompile(`className="([a-z][\w-]*(?: [a-z][\w-]*)+)"`)
	var clashes []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".tsx") || strings.Contains(path, ".test.") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range classAttr.FindAllSubmatch(src, -1) {
			parts := strings.Fields(string(m[1]))
			for _, mod := range parts[1:] {
				// Substring is not enough: ".stepi.skipped" contains
				// ".stepi.skip", so the exemption would cover the very
				// combination it is meant to catch.
				paired := regexp.MustCompile(`\.` + regexp.QuoteMeta(parts[0]) + `\.` + regexp.QuoteMeta(mod) + `([^\w-]|$)`)
				if positioned[mod] && !paired.Match(css) {
					clashes = append(clashes, path+`: "`+string(m[1])+`" — .`+mod+" positions on its own, so it will move this out of its row")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range clashes {
		t.Error(c)
	}
}
