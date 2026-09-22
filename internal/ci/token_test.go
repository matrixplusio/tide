package ci

import (
	"strings"
	"testing"
)

func TestNewTokenIsUniqueAndHashed(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		plain, hash, prefix, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case !strings.HasPrefix(plain, TokenPrefix):
			t.Fatalf("token %q does not carry the prefix", plain)
		case seen[plain]:
			t.Fatalf("token %q was minted twice", plain)
		case strings.Contains(hash, plain):
			t.Fatal("the stored hash contains the token itself")
		case !strings.HasPrefix(plain, prefix):
			t.Fatalf("prefix %q does not start token %q", prefix, plain)
		case len(prefix) >= len(plain):
			t.Fatalf("prefix %q is the whole token", prefix)
		case HashToken(plain) != hash:
			t.Fatal("hashing the same token twice gave different results")
		}
		seen[plain] = true
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct{ header, want string }{
		{"Bearer tide_ci_abc", "tide_ci_abc"},
		{"bearer tide_ci_abc", "tide_ci_abc"},
		{"BEARER  tide_ci_abc  ", "tide_ci_abc"},
		{"Basic tide_ci_abc", ""},
		{"tide_ci_abc", ""},
		{"Bearer", ""},
		{"Bearer ", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := BearerToken(tt.header); got != tt.want {
			t.Fatalf("%q: got %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestSameToken(t *testing.T) {
	if !SameToken("abc", "abc") {
		t.Fatal("equal tokens compared unequal")
	}
	if SameToken("abc", "abd") || SameToken("abc", "abcd") {
		t.Fatal("different tokens compared equal")
	}
}
