package errcode

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"tide/internal/i18n"
)

// The table in docs/api.md is the contract for clients; it must list exactly
// the codes (and HTTP statuses) defined here.
func TestDocsMatchTable(t *testing.T) {
	b, err := os.ReadFile("../../../../docs/api.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`(?m)^\| (\d+) \| (\d{3}) \|`)
	documented := map[Code]int{}
	for _, m := range row.FindAllStringSubmatch(string(b), -1) {
		code, _ := strconv.Atoi(m[1])
		status, _ := strconv.Atoi(m[2])
		documented[Code(code)] = status
	}
	for code, status := range All() {
		if got, ok := documented[code]; !ok {
			t.Errorf("code %d missing from docs/api.md", code)
		} else if got != status {
			t.Errorf("code %d: docs say HTTP %d, table says %d", code, got, status)
		}
	}
	for code := range documented {
		if !Known(code) {
			t.Errorf("docs/api.md lists unknown code %d", code)
		}
	}
}

func TestRanges(t *testing.T) {
	for code := range All() {
		if code != OK && (code < 1000 || code > 9999) {
			t.Errorf("code %d outside the 4-digit ranges", code)
		}
		// A code whose key is missing from the catalog renders as the key
		// itself, so compare against it rather than against "".
		for _, l := range i18n.Locales() {
			if msg := code.DefaultMsg(l); msg == "" || msg == string(code.Key()) {
				t.Errorf("code %d has no %s message", code, l)
			}
		}
	}
}

// Msg is empty whenever the code's default applies, so anything that shows an
// error to a user must read Text. Handlers put this text into response data
// (an upstream check's detail, a release item's error), where an empty string
// would look like "no error at all".
func TestTextFallsBackToTheDefault(t *testing.T) {
	e := New(UpstreamTimeout, "")
	if e.Msg != "" {
		t.Fatalf("a default-message error should carry no explicit Msg, got %q", e.Msg)
	}
	if got := e.Text(i18n.ZhCN); got != UpstreamTimeout.DefaultMsg(i18n.ZhCN) {
		t.Errorf("zh: %q", got)
	}
	if got := e.Text(i18n.En); got != "The upstream timed out" {
		t.Errorf("en: %q", got)
	}
	// Words the error carries itself (an upstream's own text) are not touched.
	verbatim := New(UpstreamError, "Deployment/x: rollout failed")
	if got := verbatim.Text(i18n.En); got != "Deployment/x: rollout failed" {
		t.Errorf("verbatim: %q", got)
	}
}
