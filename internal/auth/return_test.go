package auth

import "testing"

// SafeReturn is the open-redirect guard: whatever a link puts in ?return=
// ends up in a redirect after sign-in. Everything that is not a plain
// same-origin path must come back as "/".
func TestSafeReturnRefusesAnythingOffSite(t *testing.T) {
	keep := []string{
		"/",
		"/releases",
		"/releases/REL-20260921-001",
		"/services?project=acme",
		"/admin/ci#tokens",
		"/a/b/c/d",
	}
	for _, s := range keep {
		if got := SafeReturn(s); got != s {
			t.Errorf("SafeReturn(%q) = %q, want it kept", s, got)
		}
	}

	drop := []string{
		"",                                  // nothing to go back to
		"//evil.example/x",                  // protocol-relative: the browser goes off site
		"///evil.example",                   // and its variants
		"https://evil.example/x",            // absolute
		"http://evil.example",               //
		"javascript:alert(1)",               // not a path at all
		"data:text/html,<script>",           //
		`/\evil.example`,                    // backslash: some browsers read it as a separator
		`\\evil.example`,                    //
		"/releases\r\nSet-Cookie: a=b",      // header splitting, were it ever echoed into one
		"/releases\nLocation: https://evil", //
		"releases",                          // relative to wherever the browser happens to be
		"../admin",                          //
		" /releases",                        // a leading space is not a path
	}
	for _, s := range drop {
		if got := SafeReturn(s); got != "/" {
			t.Errorf("SafeReturn(%q) = %q, want %q", s, got, "/")
		}
	}
}
