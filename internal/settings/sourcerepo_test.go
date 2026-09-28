package settings

import "testing"

// A repository URL from a pipeline becomes the project path this host
// addresses it by, and only when it is on this host: a token for one GitLab
// must not be pointed at another one's paths.
func TestSourceRepoProjectOf(t *testing.T) {
	s := SourceRepo{BaseURL: "https://git.example.com/"}
	cases := map[string]string{
		"https://git.example.com/acme/order-api":              "acme/order-api",
		"https://git.example.com/acme/deep/nested/order-api/": "acme/deep/nested/order-api",
		"https://git.example.com/acme/order-api.git":          "acme/order-api",
		"https://other.example.com/acme/order-api":            "",
		"https://git.example.com/":                            "",
		"https://git.example.com/justagroup":                  "",
		"":                                                    "",
	}
	for in, want := range cases {
		if got := s.ProjectOf(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	if (SourceRepo{}).ProjectOf("https://git.example.com/acme/order-api") != "" {
		t.Error("an unconfigured host must map nothing")
	}
}
