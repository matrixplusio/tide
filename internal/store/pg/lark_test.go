package pg_test

import (
	"context"
	"slices"
	"testing"

	"tide/internal/store/pg"
	"tide/internal/testdb"
)

// A notification mentions people by the Lark id Tide keeps for them: from
// the SSO token, or as an administrator entered it. Approval rules name
// users and groups — IdP groups and Tide groups alike — so the lookup does
// too, and leaves out whoever has no id or is disabled.
func TestLarkMentionsFollowApprovalRules(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	sso := func(sub, name string, groups ...string) int64 {
		t.Helper()
		id, _, err := s.Accounts.UpsertSSOUser(ctx, sub, name, "", groups)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	bob, carol, dave, erin := sso("oidc|bob", "Bob"), sso("oidc|carol", "Carol", "Ops"), sso("oidc|dave", "Dave"), sso("oidc|erin", "Erin", "Ops")
	if err := s.Accounts.SetLarkIDFromSSO(ctx, bob, "ou_bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts.SetLarkID(ctx, carol, "carol01"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts.SetLarkID(ctx, dave, "dave01"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Access.CreateGroup(ctx, "release-approvers", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Access.AddMembers(ctx, "release-approvers", []int64{dave}); err != nil {
		t.Fatal(err)
	}
	// erin is in Ops but has no id: nobody to mention.
	_ = erin

	got, err := s.Accounts.LarkMentions(ctx, []string{"oidc|bob"}, []string{"Ops", "release-approvers"})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if !slices.Equal(ids, []string{"ou_bob", "carol01", "dave01"}) {
		t.Errorf("mentions %v, want bob (user), carol (IdP group), dave (Tide group)", ids)
	}

	if _, err := s.Accounts.SetDisabled(ctx, carol, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Accounts.LarkMentions(ctx, nil, []string{"Ops"})
	if len(got) != 0 {
		t.Errorf("a disabled user is still mentioned: %+v", got)
	}
}

// The SSO token wins when it carries an id; when it does not, the id an
// administrator entered stays. Nothing shaped unlike a Lark id is stored.
func TestLarkIDSources(t *testing.T) {
	s, owner := testdb.Setup(t)
	ctx := context.Background()
	id, _, err := s.Accounts.UpsertSSOUser(ctx, "oidc|bob", "Bob", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts.SetLarkID(ctx, id, "manual01"); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetLarkIDFromSSO(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	u, _ := s.Accounts.UserByID(ctx, id)
	if u.LarkID != "manual01" || u.LarkIDSource != "manual" {
		t.Errorf("a token without an id replaced the manual one: %q %q", u.LarkID, u.LarkIDSource)
	}
	if err := s.Accounts.SetLarkIDFromSSO(ctx, id, "ou_from_sso"); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetLarkIDFromSSO(ctx, id, "x></at><at id=all"); err != nil {
		t.Fatal(err)
	}
	u, _ = s.Accounts.UserByID(ctx, id)
	if u.LarkID != "ou_from_sso" || u.LarkIDSource != "sso" {
		t.Errorf("after SSO: %q %q", u.LarkID, u.LarkIDSource)
	}
	if _, err := s.Accounts.SetLarkID(ctx, id, "bad id"); err == nil {
		t.Error("an id with a space was accepted")
	}
	if res := owner.Exec(`UPDATE users SET lark_id = 'x y' WHERE id = ?`, id); res.Error == nil {
		t.Error("the column accepted an id with a space")
	}
	if _, err := s.Accounts.SetLarkID(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	u, _ = s.Accounts.UserByID(ctx, id)
	if u.LarkID != "" || u.LarkIDSource != "" {
		t.Errorf("cleared id left %q %q", u.LarkID, u.LarkIDSource)
	}
	_ = pg.LarkIDPattern
}
