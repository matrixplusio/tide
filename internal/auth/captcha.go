package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Login protection limits that are not tunable. Thresholds and the counting
// window come from the security settings.
const (
	captchaTTL             = 5 * time.Minute
	maxActiveCaptchasPerIP = 20
	captchaLength          = 5
	// No 0/o, 1/l/i: characters people misread.
	captchaAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"
)

var (
	ErrCaptchaRequired = errors.New("captcha required")
	ErrCaptchaInvalid  = errors.New("captcha invalid or expired")
)

// CaptchaGenerator renders an answer into an image (data URI). Tests swap it.
type CaptchaGenerator interface {
	Generate() (answer, imageDataURI string, err error)
}

// ImageCaptcha is the default generator: a server-rendered PNG, no third-party
// service (Tide runs on internal networks). Deliberately quiet visually —
// one ink colour on a transparent background so it matches the UI in light
// and dark mode — with per-glyph rotation, jitter, two strike curves and
// speckle to resist naive OCR. It is one layer; rate limits are the other.
type ImageCaptcha struct{}

func (ImageCaptcha) Generate() (string, string, error) {
	answer, err := randomString(captchaAlphabet, captchaLength)
	if err != nil {
		return "", "", err
	}
	png, err := renderCaptcha(answer)
	if err != nil {
		return "", "", err
	}
	return answer, "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

func randomString(alphabet string, n int) (string, error) {
	var b strings.Builder
	limit := big.NewInt(int64(len(alphabet)))
	for range n {
		i, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String(), nil
}

func captchaHash(id, answer string) string {
	h := sha256.Sum256([]byte(id + ":" + strings.ToLower(strings.TrimSpace(answer))))
	return hex.EncodeToString(h[:])
}

// LoginChallenge tells the sign-in form whether a captcha is needed for this
// account and client, and issues one when it is.
type LoginChallenge struct {
	CaptchaRequired bool   `json:"captchaRequired"`
	CaptchaID       string `json:"captchaId,omitempty"`
	CaptchaImage    string `json:"captchaImage,omitempty"`
}

type loginState struct {
	locked, captcha bool
}

func (s *Service) loginState(ctx context.Context, username, ip string) (loginState, error) {
	sec, err := s.Settings.Security(ctx)
	if err != nil {
		return loginState{}, err
	}
	window := time.Duration(sec.LoginWindowMinutes) * time.Minute
	userFails, err := s.PG.Accounts.LoginFailuresForUser(ctx, LocalSub(username), window)
	if err != nil {
		return loginState{}, err
	}
	ipFails, err := s.PG.Accounts.AuditCountFromIP(ctx, "auth.login.failed", ip, window)
	if err != nil {
		return loginState{}, err
	}
	return loginState{
		locked:  userFails >= int64(sec.LockAfterUserFailures) || ipFails >= int64(sec.LockAfterIPFailures),
		captcha: userFails >= int64(sec.CaptchaAfterUserFailures) || ipFails >= int64(sec.CaptchaAfterIPFailures),
	}, nil
}

func (s *Service) captcha() CaptchaGenerator {
	if s.Captcha != nil {
		return s.Captcha
	}
	return ImageCaptcha{}
}

// Challenge returns the current requirement and, if needed, a fresh captcha.
func (s *Service) Challenge(ctx context.Context, username, ip string) (*LoginChallenge, error) {
	st, err := s.loginState(ctx, username, ip)
	if err != nil {
		return nil, err
	}
	if st.locked {
		return nil, ErrTooManyTries
	}
	if !st.captcha {
		return &LoginChallenge{}, nil
	}
	active, err := s.PG.Accounts.ActiveCaptchasFromIP(ctx, ip)
	if err != nil {
		return nil, err
	}
	if active >= maxActiveCaptchasPerIP {
		return nil, ErrTooManyTries
	}
	answer, img, err := s.captcha().Generate()
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	if err := s.PG.Accounts.CreateCaptcha(ctx, id, captchaHash(id, answer), ip, captchaTTL); err != nil {
		return nil, err
	}
	return &LoginChallenge{CaptchaRequired: true, CaptchaID: id, CaptchaImage: img}, nil
}

// checkCaptcha consumes the challenge whatever the outcome, so one image can
// never be used for more than one guess.
func (s *Service) checkCaptcha(ctx context.Context, id, answer, ip string) error {
	if id == "" || answer == "" {
		return ErrCaptchaRequired
	}
	hash, issuedTo, ok, err := s.PG.Accounts.TakeCaptcha(ctx, id)
	if err != nil {
		return err
	}
	if !ok || issuedTo != ip || subtle.ConstantTimeCompare([]byte(hash), []byte(captchaHash(id, answer))) != 1 {
		return ErrCaptchaInvalid
	}
	return nil
}
