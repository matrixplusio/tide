package v1

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"tide/internal/access"
	"tide/internal/catalog"
	"tide/internal/i18n"
	"tide/internal/notify"
	"tide/internal/rbac"
	"tide/internal/release"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
	"tide/internal/settings"
	"tide/internal/store/pg"
	"tide/internal/validate"
)

// sectionDef describes one settings section: who may manage it and how to
// make, default and validate its payload.
type sectionDef struct {
	perm rbac.Permission
	make func() any
	// load returns the stored value (or its default) redacted; nil = omit.
	load func(ctx context.Context, s *settings.Store) (any, error)
}

var sections = map[string]sectionDef{
	settings.SectionUpstreams: {rbac.EnvironmentsManage, func() any { return &settings.Upstreams{} },
		func(ctx context.Context, s *settings.Store) (any, error) {
			var u settings.Upstreams
			return redactedOrNil(s.Load(ctx, settings.SectionUpstreams, &u), &u)
		}},
	settings.SectionEnvironments: {rbac.EnvironmentsManage, func() any { return &settings.Environments{} },
		func(ctx context.Context, s *settings.Store) (any, error) { return s.Environments(ctx) }},
	settings.SectionCatalog: {rbac.EnvironmentsManage, func() any { return &settings.Catalog{} },
		func(ctx context.Context, s *settings.Store) (any, error) { return s.Catalog(ctx) }},
	settings.SectionPipelineRepo: {rbac.EnvironmentsManage, func() any { return &settings.PipelineRepo{} },
		func(ctx context.Context, s *settings.Store) (any, error) {
			var r settings.PipelineRepo
			return redactedOrNil(s.Load(ctx, settings.SectionPipelineRepo, &r), &r)
		}},
	settings.SectionNotify: {rbac.NotificationsManage, func() any { return &settings.Notify{} },
		func(ctx context.Context, s *settings.Store) (any, error) {
			n, err := s.Notify(ctx)
			return settings.Redacted(&n), err
		}},
	settings.SectionOIDC: {rbac.SettingsManage, func() any { return &settings.OIDC{} },
		func(ctx context.Context, s *settings.Store) (any, error) {
			var o settings.OIDC
			return redactedOrNil(s.Load(ctx, settings.SectionOIDC, &o), &o)
		}},
	settings.SectionSecurity: {rbac.SettingsManage, func() any { return &settings.Security{} },
		func(ctx context.Context, s *settings.Store) (any, error) { return s.Security(ctx) }},
	settings.SectionRelease: {rbac.SettingsManage, func() any { return &settings.ReleasePolicy{} },
		func(ctx context.Context, s *settings.Store) (any, error) { return s.ReleasePolicy(ctx) }},
	settings.SectionSystem: {rbac.SettingsManage, func() any { return &settings.System{} },
		func(ctx context.Context, s *settings.Store) (any, error) { return s.System(ctx) }},
}

// redactedOrNil: unconfigured sections are null, not an empty default.
func redactedOrNil(err error, v any) (any, error) {
	if errors.Is(err, settings.ErrNotConfigured) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return settings.Redacted(v), nil
}

// getSettings returns every section the user may manage.
func (a *API) getSettings(c *gin.Context) {
	g, err := a.grants(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	out := gin.H{}
	for name, def := range sections {
		if !g.Has(def.perm) {
			continue
		}
		v, err := def.load(ctx, a.Settings)
		if err != nil {
			respond.Fail(c, err)
			return
		}
		out[name] = v
	}
	if len(out) == 0 {
		a.deny(c, "settings", "")
		respond.FailCode(c, errcode.Forbidden, "")
		return
	}
	respond.OK(c, out)
}

func (a *API) putSettings(c *gin.Context) {
	p := newParams(c)
	// Shape first, then the table. A section that is merely unknown still
	// answers 5001, as documented; one that is not a section name at all is
	// a malformed parameter like any other.
	section := p.path("section", reSection, "label.section")
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	def, ok := sections[section]
	if !ok {
		respond.FailCode(c, errcode.UnknownSettingsSection, "")
		return
	}
	if err := a.check(c, def.perm); err != nil {
		respond.Fail(c, err)
		return
	}
	next := def.make()
	if err := bindJSON(c, next); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	u := currentUser(c)
	if ups, ok := next.(*settings.Upstreams); ok {
		// Resolve masked tokens first so the live check uses real credentials.
		a.keepMaskedTokens(ctx, ups)
	}
	if err := a.validateSection(ctx, next); err != nil {
		respond.Fail(c, err)
		return
	}
	// Same rule as upstreams just below: credentials are checked against the
	// host before they are stored. A token that was merely saved tells nobody
	// whether it works, and the next thing it is asked to do is write to
	// somebody's repository.
	if cfg, ok := next.(*settings.PipelineRepo); ok {
		tctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
		id, err := pushTo(*cfg).Whoami(tctx)
		cancel()
		switch {
		case err != nil:
			respond.Fail(c, errcode.New(errcode.UpstreamCheckFailed, "").
				WithData(gin.H{"error": shorten(err.Error())}))
			return
		case !id.CanWrite:
			respond.Fail(c, errcode.NewKey(errcode.UpstreamCheckFailed, "s.repoReadOnly", id.Username, cfg.Project))
			return
		}
	}
	if ups, ok := next.(*settings.Upstreams); ok {
		tctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
		results := testAll(tctx, *ups)
		cancel()
		for _, r := range results {
			if !r.OK() {
				respond.Fail(c, errcode.New(errcode.UpstreamCheckFailed, "").WithData(gin.H{"results": results}))
				return
			}
		}
	}
	err := a.PG.Tx(ctx, func(tx *pg.Store) error {
		if err := a.Settings.Save(ctx, tx, u.Actor(), section, next); err != nil {
			return err
		}
		// The catalog is derived from these three, so every replica's
		// snapshot is stale the moment they change.
		switch section {
		case settings.SectionUpstreams, settings.SectionEnvironments, settings.SectionCatalog:
			return tx.Cache.Bump(ctx, pg.ScopeCatalog)
		}
		return nil
	})
	if err != nil {
		respond.Fail(c, err)
		return
	}
	switch section {
	case settings.SectionOIDC:
		a.Auth.ResetProvider()
	case settings.SectionUpstreams, settings.SectionEnvironments, settings.SectionCatalog:
		a.Hub.Reset()
	}
	respond.OK(c, nil)
}

func (a *API) testUpstreams(c *gin.Context) {
	var ups settings.Upstreams
	if err := bindJSON(c, &ups); err != nil {
		respond.Fail(c, err)
		return
	}
	a.keepMaskedTokens(c.Request.Context(), &ups)
	if err := a.validateSection(c.Request.Context(), &ups); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
	defer cancel()
	respond.OK(c, gin.H{"results": testAll(ctx, ups)})
}

type notifyTestReq struct {
	Channel settings.Channel `json:"channel"`
}

func (a *API) testNotify(c *gin.Context) {
	var req notifyTestReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	ch := &req.Channel
	trim(&ch.Name, &ch.URL)
	if prev, err := a.Settings.Notify(ctx); err == nil {
		for _, p := range prev.Channels {
			if p.Name == ch.Name {
				keepMasked(&ch.URL, p.URL)
				keepMasked(&ch.Secret, p.Secret)
			}
		}
	}
	var errs []*validate.FieldError
	validateChannel("channel", *ch, func(e *validate.FieldError) {
		if e != nil {
			errs = append(errs, e)
		}
	})
	if err := validate.Collect(errs...); err != nil {
		respond.Fail(c, err)
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := a.Notifier.Test(tctx, *ch); err != nil {
		msg := errcode.From(err).Text(i18n.From(c.Request.Context()))
		if errcode.From(err).Code == errcode.Internal {
			msg = err.Error()
		}
		respond.Fail(c, errcode.Wrap(errcode.NotifyTestFailed, err,
			i18n.T(i18n.From(c.Request.Context()), "s.notifyTestFailed", truncate(msg, maxUpstreamMsg))))
		return
	}
	_ = a.PG.Audit.Write(ctx, currentUser(c).Actor(), "settings.notify.test", "notify", "", map[string]any{"channel": ch.Name, "kind": ch.Kind})
	respond.OK(c, nil)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func keepMasked(v *string, old string) {
	if *v == settings.Masked || *v == "" {
		*v = old
	}
}

func (a *API) keepMaskedTokens(ctx context.Context, next *settings.Upstreams) {
	var prev settings.Upstreams
	if err := a.Settings.Load(ctx, settings.SectionUpstreams, &prev); err != nil {
		return
	}
	for i := range next.Items {
		n := &next.Items[i]
		for _, p := range prev.Items {
			if p.Name != n.Name {
				continue
			}
			keepMasked(&n.KargoToken, p.KargoToken)
			keepMasked(&n.ArgoCDToken, p.ArgoCDToken)
			keepMasked(&n.RegistryToken, p.RegistryToken)
		}
	}
}

var (
	reUpstreamName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	reJiraProject  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)
)

// intRange adds an error when v is outside [lo, hi].
func intRange(add func(*validate.FieldError), field string, v, lo, hi int) {
	if v < lo || v > hi {
		add(validate.FieldKey(field, "s.intRange", lo, hi))
	}
}

func (a *API) validateSection(ctx context.Context, v any) error {
	var errs []*validate.FieldError
	add := func(e *validate.FieldError) {
		if e != nil {
			errs = append(errs, e)
		}
	}
	switch s := v.(type) {
	case *settings.OIDC:
		trim(&s.Issuer, &s.ClientID, &s.GroupsClaim, &s.RedirectURL)
		add(validate.HTTPURL("issuer", s.Issuer, true, validate.BaseURL))
		add(validate.Required("clientId", s.ClientID, "label.clientId"))
		add(validate.MaxLen("clientId", s.ClientID, 255, "label.clientId"))
		add(validate.MaxLen("groupsClaim", s.GroupsClaim, 64, "label.groupsClaim"))
		if e := validate.HTTPURL("redirectUrl", s.RedirectURL, true, validate.BaseURL); e != nil {
			add(e)
		} else if !strings.HasSuffix(s.RedirectURL, "/api/v1/auth/sso/callback") {
			add(validate.FieldKey("redirectUrl", "s.redirectSuffix"))
		}
	case *settings.Upstreams:
		envs, err := a.Settings.Environments(ctx)
		if err != nil {
			return err
		}
		validateUpstreams(s, envs, add)
	case *settings.Environments:
		var ups settings.Upstreams
		if err := a.Settings.Load(ctx, settings.SectionUpstreams, &ups); err != nil && !errors.Is(err, settings.ErrNotConfigured) {
			return err
		}
		validateEnvironments(s, ups, add)
	case *settings.Notify:
		envs, err := a.Settings.Environments(ctx)
		if err != nil {
			return err
		}
		validateNotify(s, envs, add)
	case *settings.Catalog:
		validateCatalog(s, add)
	case *settings.Security:
		intRange(add, "sessionTtlMinutes", s.SessionTTLMinutes, 5, 1440)
		intRange(add, "loginWindowMinutes", s.LoginWindowMinutes, 5, 1440)
		intRange(add, "captchaAfterUserFailures", s.CaptchaAfterUserFailures, 1, 20)
		intRange(add, "captchaAfterIpFailures", s.CaptchaAfterIPFailures, 1, 100)
		intRange(add, "lockAfterUserFailures", s.LockAfterUserFailures, 2, 100)
		intRange(add, "lockAfterIpFailures", s.LockAfterIPFailures, 2, 1000)
		if s.LockAfterUserFailures <= s.CaptchaAfterUserFailures {
			add(validate.FieldKey("lockAfterUserFailures", "s.lockAboveCaptcha", s.CaptchaAfterUserFailures))
		}
		if s.LockAfterIPFailures <= s.CaptchaAfterIPFailures {
			add(validate.FieldKey("lockAfterIpFailures", "s.lockAboveCaptcha", s.CaptchaAfterIPFailures))
		}
	case *settings.ReleasePolicy:
		envs, err := a.Settings.Environments(ctx)
		if err != nil {
			return err
		}
		validateReleasePolicy(s, envs, add)
	case *settings.System:
		trim(&s.SiteName, &s.BaseURL, &s.Announcement.Text)
		add(validate.Required("siteName", s.SiteName, "label.siteName"))
		add(validate.MaxLen("siteName", s.SiteName, 32, "label.siteName"))
		add(validate.HTTPURL("baseUrl", s.BaseURL, false, validate.BaseURL))
		if s.Announcement.Level != "info" && s.Announcement.Level != "warning" {
			add(validate.FieldKey("announcement.level", "s.announceLevel"))
		}
		add(validate.MaxLen("announcement.text", s.Announcement.Text, 500, "label.announce"))
		if s.Announcement.Enabled && s.Announcement.Text == "" {
			add(validate.FieldKey("announcement.text", "s.announceTextRequired"))
		}
	case *settings.PipelineRepo:
		trim(&s.Provider, &s.BaseURL, &s.Project, &s.Branch, &s.PathPrefix)
		if s.Provider == "" {
			s.Provider = settings.ProviderGitLab
		}
		if !slices.Contains(settings.Providers, s.Provider) {
			add(validate.FieldKey("provider", "s.unknownProvider", s.Provider))
		}
		add(validate.HTTPURL("baseUrl", s.BaseURL, true, validate.BaseURL))
		// "owner/repo": Gitea addresses the two halves separately, and GitLab
		// wants the whole path with its namespace. Neither works without one.
		if s.Project == "" {
			add(validate.FieldKey("project", "s.projectRequired"))
		} else if !strings.Contains(strings.Trim(s.Project, "/"), "/") {
			add(validate.FieldKey("project", "s.projectPath"))
		}
		add(validate.Required("token", s.Token, "label.token"))
		s.PathPrefix = strings.Trim(s.PathPrefix, "/")
	default:
		return errors.New("unknown settings type")
	}
	return validate.Collect(errs...)
}

var (
	// Kubernetes label key: optional DNS prefix and a name of up to 63 chars.
	reLabelKey     = regexp.MustCompile(`^([a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?/)?[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$`)
	reLabelValue   = regexp.MustCompile(`^([A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?)?$`)
	reDimensionKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

func validateCatalog(s *settings.Catalog, add func(*validate.FieldError)) {
	trim(&s.ServiceLabel, &s.EnvLabel, &s.DomainLabel, &s.ProjectLabel, &s.BatchDimension)
	for field, v := range map[string]string{"serviceLabel": s.ServiceLabel, "envLabel": s.EnvLabel, "domainLabel": s.DomainLabel, "projectLabel": s.ProjectLabel} {
		// catalog.FromArgoProject is a source, not a label name, and would
		// never pass a label-key rule.
		if v != "" && v != catalog.FromArgoProject && !reLabelKey.MatchString(v) {
			add(validate.FieldKey(field, "s.badLabelKey"))
		}
	}
	if s.Dimensions == nil {
		s.Dimensions = []settings.Dimension{}
	}
	if len(s.Dimensions) > 5 {
		add(validate.FieldKey("dimensions", "s.maxDimensions"))
	}
	keys, labels := map[string]bool{}, map[string]bool{}
	for i := range s.Dimensions {
		d := &s.Dimensions[i]
		trim(&d.Key, &d.Name, &d.Label)
		f := func(n string) string { return "dimensions." + strconv.Itoa(i) + "." + n }
		switch {
		case d.Key == "":
			add(validate.FieldKey(f("key"), "s.dimKeyRequired"))
		case !reDimensionKey.MatchString(d.Key) || d.Key == "q" || d.Key == "domain":
			add(validate.FieldKey(f("key"), "s.dimKeyFormat"))
		case keys[d.Key]:
			add(validate.FieldKey(f("key"), "s.dimKeyDup", d.Key))
		}
		keys[d.Key] = true
		add(validate.Required(f("name"), d.Name, "label.name"))
		add(validate.MaxLen(f("name"), d.Name, 16, "label.name"))
		switch {
		case d.Label == "":
			add(validate.FieldKey(f("label"), "s.labelRequired"))
		case !reLabelKey.MatchString(d.Label):
			add(validate.FieldKey(f("label"), "s.badLabelKey"))
		case labels[d.Label]:
			add(validate.FieldKey(f("label"), "s.labelTaken", d.Label))
		}
		labels[d.Label] = true
		if d.Values == nil {
			d.Values = []settings.DimensionValue{}
		}
		if len(d.Values) > 50 {
			add(validate.FieldKey(f("values"), "s.maxValues"))
		}
		seen := map[string]bool{}
		for j := range d.Values {
			v := &d.Values[j]
			trim(&v.Value, &v.Name)
			vf := func(n string) string { return f("values") + "." + strconv.Itoa(j) + "." + n }
			switch {
			case v.Value == "" || !reLabelValue.MatchString(v.Value):
				add(validate.FieldKey(vf("value"), "s.badLabelValue"))
			case seen[v.Value]:
				add(validate.FieldKey(vf("value"), "s.valueDup", v.Value))
			}
			seen[v.Value] = true
			add(validate.MaxLen(vf("name"), v.Name, 16, "label.displayName"))
		}
	}
	if s.BatchDimension != "" {
		d, ok := s.Dimension(s.BatchDimension)
		switch {
		case !ok:
			add(validate.FieldKey("batchDimension", "s.dimMissing", s.BatchDimension))
		case len(d.Values) < 2:
			add(validate.FieldKey("batchDimension", "s.batchDimNeedsTwo"))
		}
	}
}

type labelView struct {
	Key    string         `json:"key"`
	Count  int            `json:"count"`
	Values []labelValView `json:"values"`
}

type labelValView struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// catalogLabels lists labels found on mapped Applications, most common first,
// so administrators pick keys that actually exist.
func (a *API) catalogLabels(c *gin.Context) {
	snap, err := a.Hub.Snapshot(c.Request.Context(), false)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	out := []labelView{}
	for k, vals := range snap.Labels {
		lv := labelView{Key: k, Values: []labelValView{}}
		for v, n := range vals {
			lv.Count += n
			lv.Values = append(lv.Values, labelValView{Value: v, Count: n})
		}
		slices.SortFunc(lv.Values, func(x, y labelValView) int { return y.Count - x.Count })
		if len(lv.Values) > 30 {
			lv.Values = lv.Values[:30]
		}
		out = append(out, lv)
	}
	slices.SortFunc(out, func(x, y labelView) int {
		if x.Count != y.Count {
			return y.Count - x.Count
		}
		return strings.Compare(x.Key, y.Key)
	})
	respond.OK(c, gin.H{"items": out})
}

func validateUpstreams(u *settings.Upstreams, envs settings.Environments, add func(*validate.FieldError)) {
	if len(u.Items) == 0 {
		add(validate.FieldKey("items", "s.upstreamAtLeastOne"))
	}
	names := map[string]bool{}
	for i := range u.Items {
		it := &u.Items[i]
		trim(&it.Name, &it.KargoURL, &it.ArgoCDURL, &it.RegistryURL, &it.RegistryUser, &it.GrafanaURL)
		f := func(n string) string { return "items." + strconv.Itoa(i) + "." + n }
		switch {
		case it.Name == "":
			add(validate.FieldKey(f("name"), "s.upstreamNameRequired"))
		case !reUpstreamName.MatchString(it.Name):
			add(validate.FieldKey(f("name"), "s.nameFormat"))
		case names[it.Name]:
			add(validate.FieldKey(f("name"), "s.upstreamNameDup", it.Name))
		}
		names[it.Name] = true
		add(validate.HTTPURL(f("kargoUrl"), it.KargoURL, true, validate.BaseURL))
		add(validate.Required(f("kargoToken"), it.KargoToken, "label.kargoToken"))
		add(validate.HTTPURL(f("argocdUrl"), it.ArgoCDURL, true, validate.BaseURL))
		add(validate.Required(f("argocdToken"), it.ArgoCDToken, "label.argocdToken"))
		add(validate.HTTPURL(f("registryUrl"), it.RegistryURL, false, validate.AnyURL))
		if it.RegistryToken != "" && it.RegistryUser == "" {
			add(validate.FieldKey(f("registryUser"), "s.registryUserNeeded"))
		}
		add(validate.HTTPURL(f("grafanaUrl"), it.GrafanaURL, false, validate.AnyURL))
		trim(&it.KargoExpires, &it.ArgoCDExpires, &it.RegistryExpires)
		for _, e := range []struct {
			field, value string
		}{
			{"kargoExpires", it.KargoExpires},
			{"argocdExpires", it.ArgoCDExpires},
			{"registryExpires", it.RegistryExpires},
		} {
			if e.value == "" {
				continue // optional: a credential with no recorded date is fine
			}
			// Rejecting a date far in the future catches the usual slip of
			// typing the year wrong, which would silence the warning for a
			// century instead of raising it.
			if t, err := time.Parse(settings.DateLayout, e.value); err != nil || t.After(time.Now().AddDate(10, 0, 0)) {
				add(validate.FieldKey(f(e.field), "s.expiryFormat"))
			}
		}
	}
	for _, e := range envs.Items {
		if e.Upstream != "" && !names[e.Upstream] {
			add(validate.FieldKey("items", "s.upstreamInUse", e.Upstream, e.Name))
		}
	}
}

func validateEnvironments(s *settings.Environments, ups settings.Upstreams, add func(*validate.FieldError)) {
	if len(s.Items) == 0 {
		add(validate.FieldKey("items", "s.envAtLeastOne"))
	}
	if len(s.Items) > 20 {
		add(validate.FieldKey("items", "s.maxEnvs"))
	}
	seen := map[string]bool{}
	for i := range s.Items {
		e := &s.Items[i]
		trim(&e.Name, &e.DisplayName, &e.Description, &e.Upstream, &e.PromotesFrom, &e.CI)
		f := func(n string) string { return "items." + strconv.Itoa(i) + "." + n }
		switch {
		case e.Name == "":
			add(validate.FieldKey(f("name"), "s.envNameRequired"))
		case !reEnv.MatchString(e.Name):
			add(validate.FieldKey(f("name"), "s.envNameFormat"))
		case seen[e.Name]:
			add(validate.FieldKey(f("name"), "s.envDup", e.Name))
		}
		seen[e.Name] = true
		if e.DisplayName == "" {
			e.DisplayName = e.Name
		}
		add(validate.MaxLen(f("displayName"), e.DisplayName, 32, "label.displayName"))
		add(validate.MaxLen(f("description"), e.Description, 200, "label.description"))
		if !rbac.ValidTier(e.Tier) {
			add(validate.FieldKey(f("tier"), "s.tierRequired"))
		}
		if e.Upstream != "" {
			if _, ok := ups.Named(e.Upstream); !ok {
				add(validate.FieldKey(f("upstream"), "s.upstreamMissing", e.Upstream))
			}
		}
		// The source must be listed before this environment: order is the
		// promotion order, and it rules out cycles.
		if e.PromotesFrom != "" && !seenBefore(s.Items[:i], e.PromotesFrom) {
			add(validate.FieldKey(f("promotesFrom"), "s.promotesFromOrder"))
		}
		if e.CI == "" {
			e.CI = settings.CIOff
		}
		if !slices.Contains(settings.CIModes, e.CI) {
			add(validate.FieldKey(f("ci"), "s.ciModeEnum"))
		}
	}
}

func seenBefore(items []settings.Environment, name string) bool {
	for _, it := range items {
		if it.Name == name {
			return true
		}
	}
	return false
}

func validateChannel(prefix string, ch settings.Channel, add func(*validate.FieldError)) {
	f := func(n string) string { return prefix + "." + n }
	add(validate.Required(f("name"), ch.Name, "label.name"))
	add(validate.MaxLen(f("name"), ch.Name, 32, "label.name"))
	if ch.Kind != "lark" && ch.Kind != "teams" && ch.Kind != "webhook" {
		add(validate.FieldKey(f("kind"), "s.channelKind"))
	}
	if ch.URL != settings.Masked {
		add(validate.HTTPURL(f("url"), ch.URL, true, validate.AnyURL))
	}
	if ch.Secret != "" && ch.Kind != "lark" {
		add(validate.FieldKey(f("secret"), "s.secretLarkOnly"))
	}
	add(validate.MaxLen(f("secret"), ch.Secret, 256, "label.secret"))
}

func validateNotify(s *settings.Notify, envs settings.Environments, add func(*validate.FieldError)) {
	if s.Channels == nil {
		s.Channels = []settings.Channel{}
	}
	if s.Rules == nil {
		s.Rules = []settings.NotifyRule{}
	}
	channels := map[string]bool{}
	for i := range s.Channels {
		ch := &s.Channels[i]
		trim(&ch.Name, &ch.URL, &ch.Secret)
		prefix := "channels." + strconv.Itoa(i)
		validateChannel(prefix, *ch, add)
		if channels[ch.Name] {
			add(validate.FieldKey(prefix+".name", "s.nameDup", ch.Name))
		}
		channels[ch.Name] = true
	}
	rules := map[string]bool{}
	for i := range s.Rules {
		r := &s.Rules[i]
		trim(&r.Name)
		f := func(n string) string { return "rules." + strconv.Itoa(i) + "." + n }
		add(validate.Required(f("name"), r.Name, "label.ruleName"))
		add(validate.MaxLen(f("name"), r.Name, 32, "label.ruleName"))
		if rules[r.Name] && r.Name != "" {
			add(validate.FieldKey(f("name"), "s.ruleDup", r.Name))
		}
		rules[r.Name] = true
		for _, e := range access.CheckEnvSelectors(f("envs"), r.Envs, envs) {
			add(e)
		}
		if len(r.Events) == 0 {
			add(validate.FieldKey(f("events"), "s.eventsRequired"))
		}
		for _, ev := range r.Events {
			if !slices.Contains(notify.Events, ev) {
				add(validate.FieldKey(f("events"), "s.eventUnknown", ev))
			}
		}
		if len(r.Channels) == 0 {
			add(validate.FieldKey(f("channels"), "s.channelsRequired"))
		}
		for _, c := range r.Channels {
			if !channels[c] {
				add(validate.FieldKey(f("channels"), "s.channelMissing", c))
			}
		}
	}
}

func validateReleasePolicy(s *settings.ReleasePolicy, envs settings.Environments, add func(*validate.FieldError)) {
	trim(&s.JiraBaseURL)
	intRange(add, "confirmReadSeconds", s.ConfirmReadSeconds, settings.MinConfirmReadSeconds, 120)
	intRange(add, "confirmTtlMinutes", s.ConfirmTTLMinutes, 2, 60)
	intRange(add, "executeTimeoutMinutes", s.ExecuteTimeoutMinutes, 5, 240)
	intRange(add, "minSoakMinutes", s.MinSoakMinutes, 0, 10080)
	intRange(add, "multiVersionJump", s.MultiVersionJump, 0, 100)
	if s.ConfirmTTLMinutes*60 <= s.ConfirmReadSeconds {
		add(validate.FieldKey("confirmTtlMinutes", "s.confirmTtlOrder"))
	}
	add(validate.HTTPURL("jiraBaseUrl", s.JiraBaseURL, false, validate.BaseURL))
	for field, sel := range map[string]*[]string{"jiraRequired": &s.JiraRequired, "reasonRequired": &s.ReasonRequired, "soakEnforced": &s.SoakEnforced, "versionJumpEnforced": &s.VersionJumpEnforced, "configDriftEnforced": &s.ConfigDriftEnforced} {
		if *sel == nil {
			*sel = []string{}
		}
		if len(*sel) > 0 {
			for _, e := range access.CheckEnvSelectors(field, *sel, envs) {
				add(e)
			}
		}
	}
	validateApprovals(s, envs, add)
	if s.JiraProjects == nil {
		s.JiraProjects = []string{}
	}
	if s.Freezes == nil {
		s.Freezes = []settings.Freeze{}
	}
	if len(s.JiraProjects) > 50 {
		add(validate.FieldKey("jiraProjects", "s.maxJiraProjects"))
	}
	seen := map[string]bool{}
	for i := range s.JiraProjects {
		trim(&s.JiraProjects[i])
		s.JiraProjects[i] = strings.ToUpper(s.JiraProjects[i])
		p := s.JiraProjects[i]
		f := "jiraProjects." + strconv.Itoa(i)
		switch {
		case !reJiraProject.MatchString(p):
			add(validate.FieldKey(f, "s.jiraProjectFormat"))
		case seen[p]:
			add(validate.FieldKey(f, "s.jiraProjectDup", p))
		}
		seen[p] = true
	}
	if len(s.Freezes) > 50 {
		add(validate.FieldKey("freezes", "s.maxFreezes"))
	}
	for i := range s.Freezes {
		fr := &s.Freezes[i]
		trim(&fr.Name, &fr.Reason)
		f := func(n string) string { return "freezes." + strconv.Itoa(i) + "." + n }
		add(validate.Required(f("name"), fr.Name, "label.name"))
		add(validate.MaxLen(f("name"), fr.Name, 64, "label.name"))
		add(validate.MaxLen(f("reason"), fr.Reason, 500, "label.reason"))
		for _, e := range access.CheckEnvSelectors(f("envs"), fr.Envs, envs) {
			add(e)
		}
		switch {
		case fr.StartsAt.IsZero():
			add(validate.FieldKey(f("startsAt"), "s.startRequired"))
		case fr.EndsAt.IsZero():
			add(validate.FieldKey(f("endsAt"), "s.endRequired"))
		case !fr.EndsAt.After(fr.StartsAt):
			add(validate.FieldKey(f("endsAt"), "s.endAfterStart"))
		}
	}
}

type CheckResult struct {
	Upstream string  `json:"upstream"`
	Checks   []Check `json:"checks"`
}

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (c CheckResult) OK() bool {
	for _, ch := range c.Checks {
		if !ch.OK {
			return false
		}
	}
	return true
}

func testAll(ctx context.Context, ups settings.Upstreams) []CheckResult {
	out := make([]CheckResult, len(ups.Items))
	done := make(chan struct{})
	for i, u := range ups.Items {
		go func() {
			out[i] = testUpstream(ctx, u)
			done <- struct{}{}
		}()
	}
	for range ups.Items {
		<-done
	}
	return out
}

func testUpstream(ctx context.Context, u settings.Upstream) CheckResult {
	loc := i18n.From(ctx)
	cl := catalog.Build(u, nil)
	res := CheckResult{Upstream: u.Name, Checks: []Check{}}
	add := func(name string, err error, ok string) {
		if err != nil {
			res.Checks = append(res.Checks, Check{Name: name, Detail: errcode.From(err).Text(loc)})
			return
		}
		res.Checks = append(res.Checks, Check{Name: name, OK: true, Detail: ok})
	}
	v, err := cl.Kargo.GetVersionInfo(ctx)
	add("kargo", err, i18n.T(loc, "s.checkVersion", v))
	projects, err := cl.Kargo.ListProjects(ctx)
	add("kargo token", err, i18n.T(loc, "s.checkProjects", len(projects)))
	info, err := cl.ArgoCD.UserInfo(ctx)
	if err == nil && info["loggedIn"] != true {
		err = errors.New(i18n.T(loc, "s.checkArgoRejected"))
	}
	name, _ := info["username"].(string)
	add("argocd token", err, i18n.T(loc, "s.checkIdentity", name))
	apps, err := cl.ArgoCD.ListApplications(ctx)
	add("argocd applications", err, i18n.T(loc, "s.checkApplications", len(apps)))
	if u.RegistryURL != "" {
		add("registry", cl.Registry.Ping(ctx), i18n.T(loc, "s.checkReachable"))
	}
	return res
}

func validateApprovals(s *settings.ReleasePolicy, envs settings.Environments, add func(*validate.FieldError)) {
	if s.Approvals == nil {
		s.Approvals = []settings.ApprovalPolicy{}
	}
	if len(s.Approvals) > 20 {
		add(validate.FieldKey("approvals", "s.maxApprovalRules"))
	}
	names := map[string]bool{}
	for i := range s.Approvals {
		r := &s.Approvals[i]
		f := func(name string) string { return fmt.Sprintf("approvals.%d.%s", i, name) }
		trim(&r.Name)
		switch {
		case r.Name == "":
			add(validate.FieldKey(f("name"), "s.ruleNameRequired"))
		case len([]rune(r.Name)) > 64:
			add(validate.FieldKey(f("name"), "s.ruleNameTooLong"))
		case names[r.Name]:
			add(validate.FieldKey(f("name"), "s.ruleNameDup"))
		}
		names[r.Name] = true
		if r.Envs == nil {
			r.Envs = []string{}
		}
		if len(r.Envs) == 0 {
			add(validate.FieldKey(f("envs"), "s.envsRequired"))
		} else {
			for _, e := range access.CheckEnvSelectors(f("envs"), r.Envs, envs) {
				add(e)
			}
		}
		for _, sc := range []struct {
			field string
			list  *[]string
		}{{"projects", &r.Projects}, {"types", &r.Types}} {
			if *sc.list == nil {
				*sc.list = []string{}
			}
			trimAll(*sc.list)
			for _, e := range access.CheckScope(f(sc.field), *sc.list) {
				add(e)
			}
		}
		users := 0
		switch {
		case len(r.Approvers) == 0:
			add(validate.FieldKey(f("approvers"), "s.approversRequired"))
		case len(r.Approvers) > 50:
			add(validate.FieldKey(f("approvers"), "s.maxApprovers"))
		}
		for j := range r.Approvers {
			trim(&r.Approvers[j])
			kind, v, _ := strings.Cut(r.Approvers[j], ":")
			if (kind != "user" && kind != "group") || v == "" || len(v) > 255 {
				add(validate.FieldKey(f("approvers"), "s.approverFormat"))
				break
			}
			if kind == "user" {
				users++
			}
		}
		switch r.Mode {
		case release.ApproveAny:
		case release.ApproveAll:
			if users != len(r.Approvers) {
				add(validate.FieldKey(f("mode"), "s.allModeUsersOnly"))
			}
		case release.ApproveCount:
			if r.MinApprovals < 1 || r.MinApprovals > 10 {
				add(validate.FieldKey(f("minApprovals"), "s.minApprovalsRange"))
			} else if users == len(r.Approvers) && r.MinApprovals > users {
				add(validate.FieldKey(f("minApprovals"), "s.minApprovalsTooMany"))
			}
		default:
			add(validate.FieldKey(f("mode"), "s.approvalMode"))
		}
		if r.Mode != release.ApproveCount {
			r.MinApprovals = 0
		}
		intRange(add, f("timeoutMinutes"), r.TimeoutMinutes, 10, 1440)
	}
}
