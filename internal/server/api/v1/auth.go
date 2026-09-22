package v1

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"tide/internal/auth"
	"tide/internal/i18n"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
	"tide/internal/setup"
	"tide/internal/validate"
)

func (a *API) authMethods(c *gin.Context) {
	respond.OK(c, gin.H{"initialized": a.Setup.Initialized(), "sso": a.Auth.SSOConfigured(c.Request.Context())})
}

type loginReq struct {
	Username    string `json:"username" binding:"required,max=64" label:"username"`
	Password    string `json:"password" binding:"required,max=1024" label:"password"`
	CaptchaID   string `json:"captchaId" binding:"omitempty,uuid" label:"captcha"`
	CaptchaCode string `json:"captchaCode" binding:"max=16" label:"captcha"`
}

func (r *loginReq) Normalize() { trim(&r.Username, &r.CaptchaID, &r.CaptchaCode) }

func (a *API) login(c *gin.Context) {
	var req loginReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	u, sess, err := a.Auth.PasswordLogin(c.Request.Context(), auth.LoginInput{
		Username: req.Username, Password: req.Password,
		CaptchaID: req.CaptchaID, CaptchaCode: req.CaptchaCode, ClientIP: c.ClientIP(),
	})
	if err != nil {
		respond.Fail(c, err)
		return
	}
	setCookie(c, sessionCookie, sess.Token, "/", int(sess.TTL.Seconds()), http.SameSiteLaxMode)
	respond.OK(c, gin.H{"user": u})
}

func (a *API) logout(c *gin.Context) {
	if token, err := cookie(c, sessionCookie); err == nil {
		if err := a.Auth.Logout(c.Request.Context(), token); err != nil {
			respond.Fail(c, err)
			return
		}
	}
	clearCookie(c, sessionCookie, "/")
	respond.OK(c, nil)
}

// loginChallenge tells the sign-in form whether a captcha is needed for this
// username from this client, issuing a new one each call when it is.
func (a *API) loginChallenge(c *gin.Context) {
	p := newParams(c)
	username := p.text("username", 64)
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	ch, err := a.Auth.Challenge(c.Request.Context(), username, c.ClientIP())
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, ch)
}

const ssoPath = "/api/v1/auth/sso"

// ssoLogin and ssoCallback are browser redirects, the documented exception to
// the envelope. Failures land on /login with a readable message.
func (a *API) ssoLogin(c *gin.Context) {
	// Bounded here, restricted to a site-relative path by auth.SafeReturn.
	// The length cap matters: this goes into a cookie the browser sends back.
	p := newParams(c)
	returnTo := p.text("return", 512)
	if p.err() != nil {
		returnTo = ""
	}
	authURL, flow, err := a.Auth.SSOStart(c.Request.Context(), returnTo)
	if err != nil {
		zap.L().Warn("sso start failed", zap.String("request_id", c.GetString("request_id")), zap.Error(err))
		c.Redirect(http.StatusFound, "/login?error="+url.QueryEscape(i18n.T(i18n.From(c.Request.Context()), "au.ssoUnavailable", err.Error())))
		return
	}
	setCookie(c, ssoFlowCookie, flow, ssoPath, int((10 * time.Minute).Seconds()), http.SameSiteLaxMode)
	c.Redirect(http.StatusFound, authURL)
}

func (a *API) ssoCallback(c *gin.Context) {
	flow, _ := cookie(c, ssoFlowCookie)
	clearCookie(c, ssoFlowCookie, ssoPath)
	_, sess, ret, err := a.Auth.SSOFinish(c.Request.Context(), flow, c.Request.URL.Query())
	if err != nil {
		zap.L().Warn("sso callback failed", zap.String("request_id", c.GetString("request_id")), zap.Error(err))
		c.Redirect(http.StatusFound, "/login?error="+url.QueryEscape(i18n.T(i18n.From(c.Request.Context()), "au.ssoFailed", err.Error())))
		return
	}
	setCookie(c, sessionCookie, sess.Token, "/", int(sess.TTL.Seconds()), http.SameSiteLaxMode)
	c.Redirect(http.StatusFound, ret)
}

type passwordPolicy struct {
	MinLength int `json:"minLength"`
	MaxBytes  int `json:"maxBytes"`
}

var policy = passwordPolicy{MinLength: validate.PasswordMinLen, MaxBytes: validate.PasswordMaxBytes}

func (a *API) setupState(c *gin.Context) {
	respond.OK(c, gin.H{
		"initialized":    a.Setup.Initialized(),
		"tokenVerified":  a.setupVerified(c),
		"passwordPolicy": policy,
	})
}

type setupTokenReq struct {
	Token string `json:"token" binding:"required,max=128" label:"setupToken"`
}

func (r *setupTokenReq) Normalize() { trim(&r.Token) }

func (a *API) setupToken(c *gin.Context) {
	var req setupTokenReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	if exceeded, err := a.Setup.TokenAttemptsExceeded(ctx, c.ClientIP()); err != nil || exceeded {
		if err == nil {
			err = errcode.New(errcode.RateLimited, "")
		}
		respond.Fail(c, err)
		return
	}
	if !a.Setup.CheckToken(ctx, req.Token) {
		a.Setup.AuditTokenFailure(ctx)
		respond.Fail(c, errcode.Invalid(errcode.FieldError{Field: "token", Key: "err.setupTokenInvalidHint"}))
		return
	}
	enc, err := a.Settings.Box.Encrypt("setup:" + time.Now().Add(2*time.Hour).Format(time.RFC3339))
	if err != nil {
		respond.Fail(c, err)
		return
	}
	setCookie(c, setupCookie, enc, "/api/v1/setup", int((2 * time.Hour).Seconds()), http.SameSiteStrictMode)
	respond.OK(c, nil)
}

func (a *API) setupVerified(c *gin.Context) bool {
	if a.Setup.Initialized() {
		return false
	}
	v, err := cookie(c, setupCookie)
	if err != nil {
		return false
	}
	plain, err := a.Settings.Box.Decrypt(v)
	if err != nil || len(plain) < len("setup:") || plain[:6] != "setup:" {
		return false
	}
	exp, err := time.Parse(time.RFC3339, plain[6:])
	return err == nil && time.Now().Before(exp)
}

type setupAdminReq struct {
	Username        string `json:"username" binding:"required" label:"username"`
	Name            string `json:"name" label:"displayName"`
	Password        string `json:"password" binding:"required" label:"password"`
	ConfirmPassword string `json:"confirmPassword" binding:"required" label:"confirmPassword"`
}

func (r *setupAdminReq) Normalize() { trim(&r.Username, &r.Name) }

func (r *setupAdminReq) Check() error {
	return validate.Collect(
		validate.Username("username", r.Username),
		validate.DisplayName("name", r.Name),
		validate.Password("password", r.Username, r.Password),
		validate.Confirm("confirmPassword", r.Password, r.ConfirmPassword),
	)
}

func (a *API) setupAdmin(c *gin.Context) {
	if !a.setupVerified(c) {
		respond.FailCode(c, errcode.SetupTokenRequired, "")
		return
	}
	var req setupAdminReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	u, sess, err := a.Setup.CreateAdmin(c.Request.Context(), setup.AdminInput{
		Username: req.Username, Name: req.Name, Password: req.Password, ConfirmPassword: req.ConfirmPassword,
	})
	if err != nil {
		respond.Fail(c, err)
		return
	}
	clearCookie(c, setupCookie, "/api/v1/setup")
	setCookie(c, sessionCookie, sess.Token, "/", int(sess.TTL.Seconds()), http.SameSiteLaxMode)
	respond.OK(c, gin.H{"user": u})
}
