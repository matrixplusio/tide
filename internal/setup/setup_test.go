package setup_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"tide/internal/access"
	"tide/internal/auth"
	"tide/internal/crypto"
	"tide/internal/rbac"
	"tide/internal/settings"
	"tide/internal/setup"
	"tide/internal/store/pg"
	"tide/internal/testdb"
	"tide/internal/validate"
)

const key = "ZGV2LW9ubHktZW5jcnlwdGlvbi1rZXktMzItYnl0ZXM="

func services(t *testing.T) (*setup.Service, *auth.Service) {
	s, _ := testdb.Setup(t)
	box, _ := crypto.New(key)
	set := &settings.Store{PG: s, Box: box}
	a := &auth.Service{PG: s, Settings: set, Box: box, Access: &access.Service{PG: s, Settings: set}}
	su := &setup.Service{PG: s, Settings: set, Auth: a}
	if err := su.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return su, a
}

func fields(err error) map[string]bool {
	var fe validate.Errors
	out := map[string]bool{}
	if errors.As(err, &fe) {
		for _, f := range fe {
			out[f.Field] = true
		}
	}
	return out
}

const pw = "correct horse battery"

func TestSetupValidatesAllFieldsAtOnce(t *testing.T) {
	su, _ := services(t)
	_, _, err := su.CreateAdmin(context.Background(), setup.AdminInput{Username: "Admin", Password: "short", ConfirmPassword: "shorter"})
	got := fields(err)
	for _, f := range []string{"username", "password", "confirmPassword"} {
		if !got[f] {
			t.Errorf("missing field error %q in %v", f, err)
		}
	}
	if su.Initialized() {
		t.Fatal("failed attempt must not initialize")
	}
}

func TestSetupCreatesOneAdminAndSignsIn(t *testing.T) {
	su, a := services(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make([]*auth.Session, 2)
	errs := make([]error, 2)
	for i, name := range []string{"alice", "bob"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i], errs[i] = su.CreateAdmin(ctx, setup.AdminInput{Username: name, Password: pw, ConfirmPassword: pw})
		}()
	}
	wg.Wait()
	ok := 0
	for i, err := range errs {
		switch {
		case err == nil:
			ok++
			u := a.UserFromToken(ctx, results[i].Token)
			if u == nil || u.Method != auth.MethodLocal {
				t.Fatalf("session user: %+v", u)
			}
			g, err := a.Access.Grants(ctx, &u.User)
			if err != nil || !g.Has(rbac.SettingsManage) || !g.HasEnv(rbac.ReleasesCancelAny, "anything") {
				t.Fatalf("setup admin must hold every permission: %v", err)
			}
		case errors.Is(err, setup.ErrAlreadyInitialized):
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != 1 || !su.Initialized() {
		t.Fatalf("want exactly one admin, got %d", ok)
	}
	if su.CheckToken(ctx, "anything") {
		t.Fatal("setup token must stop working after initialization")
	}
}

func TestLocalAccountRules(t *testing.T) {
	su, a := services(t)
	ctx := context.Background()
	admin, _, err := su.CreateAdmin(ctx, setup.AdminInput{Username: "admin", Password: pw, ConfirmPassword: pw})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.PasswordLogin(ctx, auth.LoginInput{Username: "admin", Password: "wrong password!!", ClientIP: "10.0.0.1"}); !errors.Is(err, auth.ErrBadCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, err := a.PasswordLogin(ctx, auth.LoginInput{Username: "nobody", Password: pw, ClientIP: "10.0.0.2"}); !errors.Is(err, auth.ErrBadCredentials) {
		t.Fatalf("unknown user must look identical: %v", err)
	}
	if err := a.SetDisabled(ctx, admin, admin.ID, true); !errors.Is(err, auth.ErrSelf) {
		t.Fatalf("disable self: %v", err)
	}
	adminBindings, err := a.PG.Access.ListBindings(ctx, pg.BindingFilter{RoleID: rbac.RoleAdmin, Subject: "user:" + admin.Sub})
	if err != nil || len(adminBindings) != 1 {
		t.Fatalf("admin binding: %v %v", adminBindings, err)
	}
	if err := a.Access.DeleteBinding(ctx, admin.Actor(), adminBindings[0].ID); !errors.Is(err, auth.ErrSelf) {
		t.Fatalf("remove own admin: %v", err)
	}
	ops, err := a.CreateUser(ctx, admin, "ops", "运维", "another long passphrase")
	if err != nil {
		t.Fatalf("create ops: %v", err)
	}
	if _, err := a.CreateUser(ctx, admin, "ops", "", "another long passphrase"); !errors.Is(err, auth.ErrUserExists) {
		t.Fatalf("duplicate: %v", err)
	}
	opsUser := &auth.User{User: *ops}
	opsBinding, err := a.Access.CreateBinding(ctx, admin.Actor(), access.BindingInput{RoleID: rbac.RoleAdmin, Subject: "user:" + ops.Sub, Envs: []string{"*"}})
	if err != nil {
		t.Fatalf("grant ops admin: %v", err)
	}
	if err := a.SetDisabled(ctx, opsUser, admin.ID, true); err != nil {
		t.Fatalf("ops disables admin: %v", err)
	}
	// ops is now the only usable admin: removing its admin role must fail,
	// and so must narrowing it to some environments.
	if err := a.Access.DeleteBinding(ctx, admin.Actor(), opsBinding.ID); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("last admin: %v", err)
	}
	if _, err := a.Access.UpdateBinding(ctx, admin.Actor(), opsBinding.ID, pg.BindingScope{Envs: []string{"tier:production"}}); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("narrow last admin: %v", err)
	}
	if err := a.ChangePassword(ctx, opsUser, "another long passphrase", "another long passphrase"); len(fields(err)) == 0 {
		t.Fatalf("same password must be a field error: %v", err)
	}
	// A disabled account's sessions stop resolving immediately.
	_, sess, err := a.PasswordLogin(ctx, auth.LoginInput{Username: "ops", Password: "another long passphrase", ClientIP: "10.0.0.3"})
	if err != nil {
		t.Fatalf("ops login: %v", err)
	}
	if err := a.SetDisabled(ctx, opsUser, admin.ID, false); err != nil {
		t.Fatalf("re-enable admin: %v", err)
	}
	if err := a.SetDisabled(ctx, admin, ops.ID, true); err != nil {
		t.Fatalf("disable ops: %v", err)
	}
	if u := a.UserFromToken(ctx, sess.Token); u != nil {
		t.Fatalf("disabled account still resolves: %+v", u)
	}
}
