package v1

import "testing"

// Scopes are written into the authorization URL as they are, so only what
// RFC 6749 allows in a scope gets through.
func TestScopeFormat(t *testing.T) {
	for _, ok := range []string{"openid", "roles", "app:tide", "https://example.com/roles"} {
		if !reScope.MatchString(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "two words", `quo"te`, `back\slash`, "new\nline", string(make([]byte, 65))} {
		if reScope.MatchString(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
