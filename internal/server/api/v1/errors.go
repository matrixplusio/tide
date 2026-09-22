package v1

import (
	"context"
	"errors"

	"tide/internal/access"
	"tide/internal/auth"
	"tide/internal/catalog"
	"tide/internal/ci"
	"tide/internal/i18n"
	"tide/internal/plan"
	"tide/internal/release"
	"tide/internal/server/api/errcode"
	"tide/internal/setup"
	"tide/internal/store/pg"
	"tide/internal/upstream"
	"tide/internal/validate"
)

// maxUpstreamMsg bounds upstream error text shown to users.
const maxUpstreamMsg = 800

func init() { errcode.Register(mapDomainError) }

// mapDomainError is the single place domain errors become API codes.
func mapDomainError(err error) *errcode.Error {
	var fes validate.Errors
	var fe *validate.FieldError
	var he *upstream.HTTPError
	var pe *plan.Error
	switch {
	case errors.As(err, &fes):
		out := make([]errcode.FieldError, len(fes))
		for i, f := range fes {
			out[i] = errcode.FieldError{Field: f.Field, Msg: f.Message, Key: f.Key, Args: f.Args}
		}
		return errcode.Invalid(out...)
	case errors.As(err, &fe):
		return errcode.Invalid(errcode.FieldError{Field: fe.Field, Msg: fe.Message, Key: fe.Key, Args: fe.Args})

	case errors.Is(err, auth.ErrBadCredentials):
		return errcode.New(errcode.InvalidCredentials, "")
	case errors.Is(err, auth.ErrTooManyTries):
		return errcode.New(errcode.LoginThrottled, "")
	case errors.Is(err, auth.ErrCaptchaRequired):
		return errcode.New(errcode.CaptchaRequired, "")
	case errors.Is(err, auth.ErrCaptchaInvalid):
		return errcode.New(errcode.CaptchaInvalid, "")
	case errors.Is(err, auth.ErrLocalAdminOnly):
		return errcode.New(errcode.LocalLoginAdmins, "")
	case errors.Is(err, auth.ErrSessionNotFound):
		return errcode.NewKey(errcode.NotFound, "au.sessionGone")
	case errors.Is(err, access.ErrRoleNotFound):
		return errcode.New(errcode.RoleNotFound, "")
	case errors.Is(err, access.ErrRoleBuiltin):
		return errcode.New(errcode.RoleBuiltin, "")
	case errors.Is(err, access.ErrRoleExists):
		return errcode.Invalid(errcode.FieldError{Field: "id", Key: errcode.RoleExists.Key()})
	case errors.Is(err, access.ErrBindingExists):
		return errcode.New(errcode.BindingExists, "")
	case errors.Is(err, access.ErrBindingNotFound):
		return errcode.New(errcode.BindingNotFound, "")
	case errors.Is(err, access.ErrGroupNotFound):
		return errcode.New(errcode.GroupNotFound, "")
	case errors.Is(err, access.ErrGroupExists):
		return errcode.Invalid(errcode.FieldError{Field: "name", Key: errcode.GroupExists.Key()})
	case errors.Is(err, auth.ErrUserExists):
		return errcode.Invalid(errcode.FieldError{Field: "username", Key: errcode.UserExists.Key()})
	case errors.Is(err, auth.ErrUserNotFound):
		return errcode.New(errcode.UserNotFound, "")
	case errors.Is(err, auth.ErrSelf):
		return errcode.New(errcode.CannotModifySelf, "")
	case errors.Is(err, auth.ErrLastAdmin):
		return errcode.New(errcode.LastAdminProtected, "")
	case errors.Is(err, auth.ErrProfileManaged):
		return errcode.New(errcode.SSOProfileManaged, "")
	case errors.Is(err, auth.ErrNotLocal):
		return errcode.New(errcode.SSOPasswordManaged, "")
	case errors.Is(err, setup.ErrAlreadyInitialized):
		return errcode.New(errcode.AlreadyInitialized, "")

	case errors.Is(err, release.ErrNotFound):
		return errcode.New(errcode.ReleaseNotFound, "")
	case errors.Is(err, release.ErrFrozen):
		return errcode.Wrap(errcode.EnvironmentFrozen, err, frozenMsg(err))
	case errors.Is(err, release.ErrTargetBusy):
		return errcode.New(errcode.TargetBusy, "")
	case errors.Is(err, release.ErrTooEarly):
		return errcode.Wrap(errcode.ConfirmTooEarly, err, "")
	case errors.Is(err, release.ErrConfirmExpired):
		return errcode.New(errcode.ConfirmExpired, "")
	case errors.Is(err, release.ErrApprovalExpired):
		return errcode.New(errcode.ApprovalExpired, "")
	case errors.Is(err, release.ErrSelfApproval):
		return errcode.New(errcode.SelfApproval, "")
	case errors.Is(err, release.ErrNotApprover):
		return errcode.New(errcode.NotApprover, "")
	case errors.Is(err, release.ErrAlreadyDecided):
		return errcode.New(errcode.AlreadyDecided, "")
	case errors.Is(err, release.ErrInvalidTransition):
		return errcode.Wrap(errcode.InvalidTransition, err, "")
	case errors.As(err, &pe):
		return errcode.NewKey(errcode.ArtifactUnavailable, pe.Key, pe.Args...)

	case errors.Is(err, pg.ErrCITokenUnknown):
		return errcode.New(errcode.CITokenInvalid, "")
	case errors.Is(err, ci.ErrCIDisabled):
		return errcode.NewKey(errcode.CIDisabled, "ci.disabledHere")
	case errors.Is(err, ci.ErrEnvUnknown):
		return errcode.NewKey(errcode.NotFound, "ci.envUnknown")
	case errors.Is(err, ci.ErrNotDeployed):
		return errcode.New(errcode.ServiceNotDeployed, "")
	case errors.Is(err, ci.ErrNotDirect):
		return errcode.NewKey(errcode.CIDisabled, "ci.notDirect")

	case errors.Is(err, catalog.ErrNoUpstreams):
		return errcode.New(errcode.NoUpstreams, "")
	case errors.Is(err, plan.ErrNotManaged):
		return errcode.New(errcode.NotManagedByKargo, "")
	case errors.As(err, &he):
		// Shown verbatim (product rule: original upstream text), bounded.
		msg := he.Error()
		if r := []rune(msg); len(r) > maxUpstreamMsg {
			msg = string(r[:maxUpstreamMsg]) + "…"
		}
		return errcode.Wrap(errcode.UpstreamError, err, msg)
	case errors.Is(err, context.DeadlineExceeded):
		return errcode.Wrap(errcode.UpstreamTimeout, err, "")
	}
	return nil
}

func frozenMsg(err error) string {
	var fe *release.FrozenError
	if !errors.As(err, &fe) {
		return ""
	}
	// No end time here: the server's time zone is not the reader's. The page
	// shows the window in local time from /me.
	msg := i18n.T(i18n.Default, "r.frozen", fe.Env, fe.Name)
	if fe.Reason != "" {
		msg += "：" + fe.Reason
	}
	return msg
}
