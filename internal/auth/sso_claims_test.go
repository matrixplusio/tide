package auth

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func claimsOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// IdPs put an application's roles in different places and shapes; each of
// these has to come out as the same list of names a role binding can match.
func TestRolesAreReadInTheShapesIdPsSendThem(t *testing.T) {
	cases := []struct {
		name, claim, raw string
		want             []string
	}{
		{"a list of names", "roles", `{"roles":["tide-dev","tide-ops"]}`, []string{"tide-dev", "tide-ops"}},
		{"one name", "role", `{"role":"tide-ops"}`, []string{"tide-ops"}},
		{"comma separated", "roles", `{"roles":"tide-dev, tide-ops"}`, []string{"tide-dev", "tide-ops"}},
		{"space separated", "roles", `{"roles":"tide-dev tide-ops"}`, []string{"tide-dev", "tide-ops"}},
		{"objects with a code", "roles", `{"roles":[{"code":"tide-dev","name":"开发"},{"name":"运维"}]}`, []string{"tide-dev", "运维"}},
		{"nested", "resource_access.tide.roles", `{"resource_access":{"tide":{"roles":["tide-dev"]}}}`, []string{"tide-dev"}},
		{"a dotted name that is a field of its own", "https://example.com/roles", `{"https://example.com/roles":["tide-dev"]}`, []string{"tide-dev"}},
		{"duplicates and blanks", "roles", `{"roles":["tide-dev"," ","tide-dev"]}`, []string{"tide-dev"}},
		{"the default claim", "", `{"groups":["tide-dev"]}`, []string{"tide-dev"}},
		{"missing", "roles", `{"groups":["tide-dev"]}`, []string{}},
		{"a shape nobody sends", "roles", `{"roles":{"tide":true}}`, []string{}},
	}
	for _, c := range cases {
		if got := groups(claimsOf(t, c.raw), c.claim); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// What an IdP sends is stored per user; it must not be able to make that
// arbitrarily large.
func TestRolesAreBounded(t *testing.T) {
	many := make([]string, 0, 300)
	for i := range 300 {
		many = append(many, "r"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	raw, _ := json.Marshal(map[string]any{"roles": append(many, strings.Repeat("y", 300))})
	got := groups(claimsOf(t, string(raw)), "roles")
	if len(got) > maxGroups {
		t.Errorf("%d roles kept, want at most %d", len(got), maxGroups)
	}
	for _, g := range got {
		if len(g) > maxGroupLength {
			t.Errorf("kept a role of %d bytes", len(g))
		}
	}
}

func TestOnlyClaimNamesAreRecorded(t *testing.T) {
	got := claimNames(claimsOf(t, `{"sub":"u1","email":"a@example.com","roles":["tide-dev"]}`))
	if !reflect.DeepEqual(got, []string{"email", "roles", "sub"}) {
		t.Errorf("got %q", got)
	}
}

func TestTheLarkIDClaimIsReadLikeTheGroupsClaim(t *testing.T) {
	for claim, want := range map[string]string{
		"lark_user_id":     "a1b2c3",
		"ext.lark.open_id": "ou_123",
		"missing":          "",
		"not_a_string":     "",
	} {
		got := larkIDClaim(claimsOf(t, `{"lark_user_id":" a1b2c3 ","ext":{"lark":{"open_id":"ou_123"}},"not_a_string":42}`), claim)
		if got != want {
			t.Errorf("larkIDClaim(%q) = %q, want %q", claim, got, want)
		}
	}
}
