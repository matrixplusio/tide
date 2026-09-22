package errcode

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The web client mirrors this table in web/src/lib/errcode.ts. The constant
// names differ on purpose (the client reads in its own idiom), but a code that
// drifts apart would send users a wrong message, so the numbers must match
// exactly, both ways.
func TestWebMirrorHasTheSameCodes(t *testing.T) {
	const mirror = "../../../../web/src/lib/errcode.ts"
	src, err := os.ReadFile(mirror)
	if err != nil {
		t.Fatalf("read %s: %v", mirror, err)
	}
	web := map[int]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9]*)\s*:\s*(\d+)\s*,`).FindAllStringSubmatch(string(src), -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("%s: %v", m[1], err)
		}
		web[n] = true
	}
	if len(web) == 0 {
		t.Fatalf("%s: parsed no codes; has the file's shape changed?", mirror)
	}
	for code := range table {
		if !web[int(code)] {
			t.Errorf("code %d is served but missing from %s", code, mirror)
		}
		delete(web, int(code))
	}
	delete(web, int(OK))
	for code := range web {
		t.Errorf("code %d is in %s but no longer served", code, mirror)
	}
}
