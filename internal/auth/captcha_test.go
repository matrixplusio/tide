package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tide/internal/access"
	"tide/internal/audit"
	"tide/internal/auth"
	"tide/internal/crypto"
	"tide/internal/settings"
	"tide/internal/store/pg"
	"tide/internal/testdb"
)

type fixedCaptcha struct{ answer string }

func (f fixedCaptcha) Generate() (string, string, error) {
	return f.answer, "data:image/png;base64,AAAA", nil
}

const pw = "correct horse battery"

var defaults = settings.DefaultSecurity()

func service(t *testing.T) *auth.Service {
	t.Helper()
	s, _ := testdb.Setup(t)
	box, _ := crypto.New("ZGV2LW9ubHktZW5jcnlwdGlvbi1rZXktMzItYnl0ZXM=")
	set := &settings.Store{PG: s, Box: box}
	a := &auth.Service{PG: s, Settings: set, Box: box, Access: &access.Service{PG: s, Settings: set}, Captcha: fixedCaptcha{"k7m2x"}}
	err := s.Tx(context.Background(), func(tx *pg.Store) error {
		_, err := auth.CreateLocalUser(context.Background(), tx, "ops", "运维", pw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// ctxFrom mirrors the HTTP layer: audit records carry the client IP, which
// is what per-IP counting reads.
func ctxFrom(ip string) context.Context {
	return audit.WithMeta(context.Background(), audit.Meta{ClientIP: ip})
}

func login(a *auth.Service, ip, password, id, code string) error {
	_, _, err := a.PasswordLogin(ctxFrom(ip), auth.LoginInput{Username: "ops", Password: password, ClientIP: ip, CaptchaID: id, CaptchaCode: code})
	return err
}

func TestCaptchaAfterFailures(t *testing.T) {
	a := service(t)
	ip := "10.0.0.1"
	for i := range defaults.CaptchaAfterUserFailures {
		ch, err := a.Challenge(ctxFrom(ip), "ops", ip)
		if err != nil || ch.CaptchaRequired {
			t.Fatalf("attempt %d: no captcha expected yet: %+v %v", i, ch, err)
		}
		if err := login(a, ip, "wrong password!!", "", ""); !errors.Is(err, auth.ErrBadCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}

	// Now even the right password is refused without a solved captcha, and
	// the refusal does not reveal whether the password was right.
	if err := login(a, ip, pw, "", ""); !errors.Is(err, auth.ErrCaptchaRequired) {
		t.Fatalf("missing captcha: %v", err)
	}
	ch, err := a.Challenge(ctxFrom(ip), "ops", ip)
	if err != nil || !ch.CaptchaRequired || ch.CaptchaID == "" || !strings.HasPrefix(ch.CaptchaImage, "data:image/png") {
		t.Fatalf("challenge: %+v %v", ch, err)
	}
	if err := login(a, ip, pw, ch.CaptchaID, "wrong"); !errors.Is(err, auth.ErrCaptchaInvalid) {
		t.Fatalf("wrong captcha: %v", err)
	}
	// One-time: the same challenge cannot be retried with the right answer.
	if err := login(a, ip, pw, ch.CaptchaID, "k7m2x"); !errors.Is(err, auth.ErrCaptchaInvalid) {
		t.Fatalf("reused captcha: %v", err)
	}
	// Bound to the client that requested it.
	ch, _ = a.Challenge(ctxFrom(ip), "ops", ip)
	if err := login(a, "10.9.9.9", pw, ch.CaptchaID, "k7m2x"); err == nil {
		t.Fatal("captcha from another IP must not work")
	}
	// Case-insensitive, then success resets the account's counter.
	ch, _ = a.Challenge(ctxFrom(ip), "ops", ip)
	if err := login(a, ip, pw, ch.CaptchaID, "K7M2X"); err != nil {
		t.Fatalf("right captcha + password: %v", err)
	}
	if ch, _ := a.Challenge(ctxFrom("10.0.0.50"), "ops", "10.0.0.50"); ch.CaptchaRequired {
		t.Fatal("success must reset the per-account counter")
	}
}

func TestLockAfterManyFailures(t *testing.T) {
	a := service(t)
	for i := range defaults.LockAfterUserFailures {
		ip := "10.1.0." + string(rune('a'+i)) // spread over IPs: this is the per-account limit
		id, code := "", ""
		if i >= defaults.CaptchaAfterUserFailures {
			ch, err := a.Challenge(ctxFrom(ip), "ops", ip)
			if err != nil {
				t.Fatal(err)
			}
			id, code = ch.CaptchaID, "k7m2x"
		}
		if err := login(a, ip, "wrong password!!", id, code); !errors.Is(err, auth.ErrBadCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := login(a, "10.2.0.1", pw, "", ""); !errors.Is(err, auth.ErrTooManyTries) {
		t.Fatalf("locked account: %v", err)
	}
	if _, err := a.Challenge(ctxFrom("10.2.0.1"), "ops", "10.2.0.1"); !errors.Is(err, auth.ErrTooManyTries) {
		t.Fatalf("locked challenge: %v", err)
	}
}

func TestImageCaptchaRenders(t *testing.T) {
	answer, img, err := auth.ImageCaptcha{}.Generate()
	if err != nil || len(answer) != 5 || !strings.HasPrefix(img, "data:image/png;base64,") || len(img) < 500 {
		t.Fatalf("answer=%q len(img)=%d err=%v", answer, len(img), err)
	}
	if strings.ContainsAny(answer, "01ilo") {
		t.Fatalf("ambiguous characters in %q", answer)
	}
}
