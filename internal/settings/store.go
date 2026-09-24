// Package settings reads and writes the settings table. Everything except
// DATABASE_URL and ENCRYPTION_KEY lives here and is edited through the UI.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"tide/internal/audit"
	"tide/internal/crypto"
	"tide/internal/rbac"
	"tide/internal/release"
	"tide/internal/store/pg"
)

type OIDC struct {
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret" secret:"true"`
	GroupsClaim  string   `json:"groupsClaim"`
	Scopes       []string `json:"scopes,omitempty"`
	// RedirectURL is the externally visible callback, e.g. https://tide.example.com/auth/callback.
	RedirectURL string `json:"redirectUrl"`
}

// Upstream is one Kargo + Argo CD + Registry trio, e.g. "onprem". Which
// environments it serves is decided by Environments.
type Upstream struct {
	Name          string `json:"name"`
	KargoURL      string `json:"kargoUrl"`
	KargoToken    string `json:"kargoToken" secret:"true"`
	ArgoCDURL     string `json:"argocdUrl"`
	ArgoCDToken   string `json:"argocdToken" secret:"true"`
	RegistryURL   string `json:"registryUrl"`
	RegistryUser  string `json:"registryUser"`
	RegistryToken string `json:"registryToken" secret:"true"`
	InsecureTLS   bool   `json:"insecureTls"`
	// The day each credential stops working, "2026-10-22", or empty when
	// nobody wrote it down. Tide does not read an expiry out of the token
	// itself: every issuer encodes it differently — a JWT carries `exp`, a
	// registry token needs an API call — and a date somebody types once when
	// they create the credential costs nothing and cannot be wrong about a
	// format Tide has not met yet.
	KargoExpires    string `json:"kargoExpires,omitempty"`
	ArgoCDExpires   string `json:"argocdExpires,omitempty"`
	RegistryExpires string `json:"registryExpires,omitempty"`
	// GrafanaURL is an optional template; {service} and {env} are substituted.
	GrafanaURL string `json:"grafanaUrl,omitempty"`
}

// ExpiryWarnDays is how early a credential's expiry starts being mentioned.
// Two weeks is long enough to get a new token issued through whatever process
// owns it, and short enough that a credential rotated monthly is not
// permanently complaining.
const ExpiryWarnDays = 14

// DateLayout is how expiry dates are written: a day, no time and no zone. A
// credential expires on a date wherever you read it from, and pretending to
// know the hour would only invite arguments about which zone it was in.
const DateLayout = "2006-01-02"

// CredentialExpiry is one credential whose recorded expiry is near or past.
type CredentialExpiry struct {
	Upstream string `json:"upstream"`
	// Kind is "kargo", "argocd" or "registry" — which credential, so the
	// message can say what to go and reissue.
	Kind    string `json:"kind"`
	Expires string `json:"expires"`
	// Days remaining, negative once the date has gone by.
	Days int `json:"days"`
}

// CredentialDates carries the three recorded expiry dates away from the
// tokens they belong to, so the catalog can warn about them without holding
// the credentials themselves.
type CredentialDates struct {
	Kargo, ArgoCD, Registry string
}

func (u Upstream) CredentialDates() CredentialDates {
	return CredentialDates{Kargo: u.KargoExpires, ArgoCD: u.ArgoCDExpires, Registry: u.RegistryExpires}
}

// Expiring reports the credentials expiring within `within` days, soonest
// first. A credential with no recorded date says nothing: Tide knows nothing
// about it, and a guess would be worse than silence.
func (d CredentialDates) Expiring(upstream string, now time.Time, within int) []CredentialExpiry {
	today := now.UTC().Truncate(24 * time.Hour)
	var out []CredentialExpiry
	for _, c := range []struct{ kind, date string }{
		{"kargo", d.Kargo}, {"argocd", d.ArgoCD}, {"registry", d.Registry},
	} {
		if c.date == "" {
			continue
		}
		t, err := time.Parse(DateLayout, c.date)
		if err != nil {
			continue // validation rejects these on save; an old row is not worth a crash
		}
		days := int(t.Sub(today).Hours() / 24)
		if days > within {
			continue
		}
		out = append(out, CredentialExpiry{Upstream: upstream, Kind: c.kind, Expires: c.date, Days: days})
	}
	slices.SortFunc(out, func(a, b CredentialExpiry) int { return a.Days - b.Days })
	return out
}

type Upstreams struct {
	Items []Upstream `json:"items"`
}

// PipelineRepo is where generated Kargo pipelines are committed. Tide writes
// text through the host's API rather than driving git: it already speaks HTTP
// to every upstream, and a git binary in the image would need a working tree,
// an ssh agent and a credential helper to do the same job.
type PipelineRepo struct {
	// Provider is which API to speak; empty means GitLab. The two hosts
	// disagree about enough — content encoding, whether an update needs the
	// old blob's id, how a branch is created — that guessing from the URL
	// would be guessing about the part that breaks.
	Provider string `json:"provider,omitempty"`
	// BaseURL is the host, e.g. https://gitlab.example.com.
	BaseURL string `json:"baseUrl"`
	// Project is the path with namespace, e.g. "devops/k8s-pipelines".
	Project string `json:"project"`
	// Branch to commit on. It is created from the default branch when absent,
	// so pointing this at a review branch costs nothing and keeps generated
	// configuration out of the default branch until somebody has read it.
	Branch string `json:"branch"`
	// PathPrefix is prepended to every generated file's path.
	PathPrefix string `json:"pathPrefix,omitempty"`
	// Token needs api scope: writing a commit is not a read.
	Token string `json:"token" secret:"true"`
	// ImageStrategy is how Kargo picks the newest image. Kargo's own default
	// is SemVer, which finds nothing at all when tags are not semantic
	// versions — and finding nothing looks exactly like a credential problem.
	ImageStrategy string `json:"imageStrategy,omitempty"`
	// TagPattern keeps non-build tags out of the running. Under Lexical a tag
	// like "cache" or "latest" sorts above any digit and would be chosen as
	// the newest image.
	TagPattern string `json:"tagPattern,omitempty"`
	// BareDomain names Kargo projects after the business domain alone
	// ("base") instead of after the line and the domain ("acme-base"). A
	// Kargo project is a cluster-scoped namespace, so the prefixed form is
	// the safe default: two business lines both having a "base" domain is
	// ordinary, and the second one to be generated would collide.
	BareDomain bool `json:"bareDomain,omitempty"`
	// ProjectNamePrefix goes in front of every generated Kargo project name.
	//
	// A Kargo project creates a cluster-scoped namespace of the same name, so
	// the names it takes are names nothing else can have. Left to the domain
	// alone they are exactly the names a cluster reserves for the workloads
	// themselves — "base", "orders" — and an environment that later wants one
	// finds it occupied by a project holding no workloads at all. A prefix
	// keeps the two apart; "kargo-" is the obvious one.
	//
	// Changing it renames every project, which means the old ones are deleted
	// and new ones created. The Applications' authorized-stage annotations
	// name the project too, and Kargo refuses a promotion whose annotation
	// does not match — without saying that is why. So the annotations have to
	// move first.
	ProjectNamePrefix string `json:"projectNamePrefix,omitempty"`
}

// Repository providers Tide can push to.
const (
	ProviderGitLab = "gitlab"
	ProviderGitea  = "gitea"
)

var Providers = []string{ProviderGitLab, ProviderGitea}

// How Kargo orders the tags it discovers.
const (
	// StrategyLexical suits tags that begin with a sortable timestamp, where
	// dictionary order is chronological.
	StrategyLexical = "Lexical"
	StrategySemVer  = "SemVer"
	// StrategyNewestBuild reads each image's build time, which means one
	// manifest fetch per tag per warehouse — expensive at a few hundred
	// warehouses, and unnecessary when the tag already sorts.
	StrategyNewestBuild = "NewestBuild"
	StrategyDigest      = "Digest"
)

var ImageStrategies = []string{StrategyLexical, StrategySemVer, StrategyNewestBuild, StrategyDigest}

// DefaultTagPattern admits only tags that start with a digit, which leaves
// out "latest", "cache", "main" and branch names. Without it the first
// Lexical discovery would pick whichever of those sorts highest.
const DefaultTagPattern = `^[0-9]`

// Selection is the image strategy and tag filter with the defaults applied.
func (p PipelineRepo) Selection() (strategy, pattern string) {
	strategy, pattern = p.ImageStrategy, p.TagPattern
	if strategy == "" {
		strategy = StrategyLexical
	}
	if pattern == "" {
		pattern = DefaultTagPattern
	}
	return strategy, pattern
}

// Host is Provider with the default filled in.
func (p PipelineRepo) Host() string {
	if p.Provider == "" {
		return ProviderGitLab
	}
	return p.Provider
}

// Configured reports whether a push can even be attempted.
func (p PipelineRepo) Configured() bool {
	return p.BaseURL != "" && p.Project != "" && p.Token != ""
}

func (u Upstreams) Named(name string) (Upstream, bool) {
	for _, it := range u.Items {
		if it.Name == name {
			return it, true
		}
	}
	return Upstream{}, false
}

// Environment is one deployment environment. The order of Environments.Items
// is the promotion chain.
type Environment struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Tier        string `json:"tier"` // rbac.Tier*
	Description string `json:"description"`
	// Upstream serving this environment; empty while not connected.
	Upstream string `json:"upstream"`
	// PromotesFrom names the environment whose verification this one relies
	// on when they live in different Kargo projects (another site): Tide then
	// only lets through images whose digest was verified there. Empty when
	// Kargo itself links the stages, or the environment takes CI builds.
	PromotesFrom string `json:"promotesFrom"`
	// CI decides what a build pipeline's webhook may do here: CIOff refuses
	// it, CIApprove leaves the release for a person to confirm, CIAuto starts
	// it without one. An approval rule still applies under CIAuto.
	CI string `json:"ci"`
}

// CI modes for Environment.CI. Empty means CIOff: an environment takes CI
// releases only once somebody has said so.
const (
	CIOff     = "off"
	CIApprove = "approve"
	CIAuto    = "auto"
)

var CIModes = []string{CIOff, CIApprove, CIAuto}

// CIMode is e.CI with the default filled in.
func (e Environment) CIMode() string {
	if e.CI == "" {
		return CIOff
	}
	return e.CI
}

type Environments struct {
	Items []Environment `json:"items"`
}

func (e Environments) Order() []string {
	out := make([]string, len(e.Items))
	for i, it := range e.Items {
		out[i] = it.Name
	}
	return out
}

func (e Environments) Named(name string) (Environment, bool) {
	for _, it := range e.Items {
		if it.Name == name {
			return it, true
		}
	}
	return Environment{}, false
}

// Catalog says how Argo CD Applications map onto Tide's service catalog.
// Everything is read from Application labels; nothing is hard-coded to one
// organisation's conventions.
type Catalog struct {
	// ServiceLabel, EnvLabel and DomainLabel override the name-based defaults
	// (service = app name minus "-<env>", domain = namespace minus "-<env>").
	ServiceLabel string `json:"serviceLabel"`
	EnvLabel     string `json:"envLabel"`
	DomainLabel  string `json:"domainLabel"`
	// ProjectLabel groups domains into projects; without it the Kargo project
	// managing the Application is used. Batch releases stay within a project.
	ProjectLabel string `json:"projectLabel"`
	// Dimensions become filters on the services page, e.g. type or tier.
	Dimensions []Dimension `json:"dimensions"`
	// BatchDimension names the dimension (by key) that batch releases must
	// not mix, e.g. "role". Its configured value order is the required order:
	// a later value waits until earlier values have nothing in flight.
	BatchDimension string `json:"batchDimension"`
}

type Dimension struct {
	Key   string `json:"key"`   // URL-safe id, e.g. "role"
	Name  string `json:"name"`  // shown to people, e.g. "kind"
	Label string `json:"label"` // Application label to read, e.g. "example.com/role"
	// Values fixes order and display names; values found on Applications but
	// not listed here still show up, after these, under their raw value.
	Values []DimensionValue `json:"values"`
}

// Dimension returns the dimension with key, if configured.
func (c Catalog) Dimension(key string) (Dimension, bool) {
	for _, d := range c.Dimensions {
		if d.Key == key {
			return d, true
		}
	}
	return Dimension{}, false
}

// ValueName is the display name of v, or v itself.
func (d Dimension) ValueName(v string) string {
	for _, x := range d.Values {
		if x.Value == v && x.Name != "" {
			return x.Name
		}
	}
	return v
}

// Rank is v's position in the configured order, or -1 when not configured.
func (d Dimension) Rank(v string) int {
	for i, x := range d.Values {
		if x.Value == v {
			return i
		}
	}
	return -1
}

type DimensionValue struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

func DefaultCatalog() Catalog {
	return Catalog{ServiceLabel: "tide.io/service", EnvLabel: "tide.io/env", DomainLabel: "tide.io/domain", ProjectLabel: "tide.io/project", Dimensions: []Dimension{}}
}

type Notify struct {
	Channels []Channel    `json:"channels"`
	Rules    []NotifyRule `json:"rules"`
}

type Channel struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // lark / teams / webhook
	URL     string `json:"url" secret:"true"`
	Secret  string `json:"secret" secret:"true"` // lark signing secret, optional
	Enabled bool   `json:"enabled"`
}

type NotifyRule struct {
	Name     string   `json:"name"`
	Enabled  bool     `json:"enabled"`
	Envs     []string `json:"envs"`   // env selectors: "*", "tier:<tier>", "<env>"
	Events   []string `json:"events"` // release.started / succeeded / failed / cancelled
	Channels []string `json:"channels"`
}

func DefaultNotify() Notify { return Notify{Channels: []Channel{}, Rules: []NotifyRule{}} }

// Security is sign-in protection and session lifetime.
type Security struct {
	SessionTTLMinutes        int  `json:"sessionTtlMinutes"`
	LoginWindowMinutes       int  `json:"loginWindowMinutes"`
	CaptchaAfterUserFailures int  `json:"captchaAfterUserFailures"`
	CaptchaAfterIPFailures   int  `json:"captchaAfterIpFailures"`
	LockAfterUserFailures    int  `json:"lockAfterUserFailures"`
	LockAfterIPFailures      int  `json:"lockAfterIpFailures"`
	LocalLoginAdminsOnly     bool `json:"localLoginAdminsOnly"`
}

func DefaultSecurity() Security {
	return Security{SessionTTLMinutes: 60, LoginWindowMinutes: 15, CaptchaAfterUserFailures: 3, CaptchaAfterIPFailures: 5,
		LockAfterUserFailures: 10, LockAfterIPFailures: 30}
}

// ReleasePolicy tunes the release flow. ConfirmReadSeconds can only be raised
// above the product minimum of 10 seconds.
type ReleasePolicy struct {
	ConfirmReadSeconds    int    `json:"confirmReadSeconds"`
	ConfirmTTLMinutes     int    `json:"confirmTtlMinutes"`
	ExecuteTimeoutMinutes int    `json:"executeTimeoutMinutes"`
	MinSoakMinutes        int    `json:"minSoakMinutes"`   // a candidate that ran upstream shorter than this is flagged
	MultiVersionJump      int    `json:"multiVersionJump"` // flag when the target is more than this many builds ahead
	JiraBaseURL           string `json:"jiraBaseUrl"`
	// JiraRequired selects the environments where a Jira ticket is mandatory
	// ("*", "tier:<tier>", env names). Empty: optional everywhere.
	// SoakEnforced / VersionJumpEnforced select the environments where the
	// minSoakMinutes / multiVersionJump thresholds block a release instead of
	// only being highlighted on the confirmation sheet. Same selector syntax.
	// Approvals: the first rule whose envs match the release's environment
	// sends a confirmed release to approval before it executes.
	Approvals           []ApprovalPolicy `json:"approvals"`
	SoakEnforced        []string         `json:"soakEnforced"`
	VersionJumpEnforced []string         `json:"versionJumpEnforced"`
	// ConfigDriftEnforced selects the environments where an upgrade is
	// refused while the Application has unsynced git changes, unless the
	// creator explicitly sends them along (withConfig).
	ConfigDriftEnforced []string `json:"configDriftEnforced"`
	JiraRequired        []string `json:"jiraRequired"`
	// ReasonRequired selects the environments where a reason is mandatory.
	ReasonRequired []string `json:"reasonRequired"`
	JiraProjects   []string `json:"jiraProjects"` // allowed project keys; empty = any
	Freezes        []Freeze `json:"freezes"`
}

type Freeze struct {
	Name     string    `json:"name"`
	Envs     []string  `json:"envs"`
	StartsAt time.Time `json:"startsAt"`
	EndsAt   time.Time `json:"endsAt"`
	Reason   string    `json:"reason"`
}

func DefaultReleasePolicy() ReleasePolicy {
	return ReleasePolicy{ConfirmReadSeconds: 10, ConfirmTTLMinutes: 10, ExecuteTimeoutMinutes: 15, MinSoakMinutes: 30,
		MultiVersionJump: 3, Approvals: []ApprovalPolicy{}, SoakEnforced: []string{}, VersionJumpEnforced: []string{}, ConfigDriftEnforced: []string{}, JiraRequired: []string{"tier:production"}, ReasonRequired: []string{"*"}, JiraProjects: []string{}, Freezes: []Freeze{}}
}

func (p ReleasePolicy) ConfirmRead() time.Duration {
	return time.Duration(max(p.ConfirmReadSeconds, MinConfirmReadSeconds)) * time.Second
}

func (p ReleasePolicy) ConfirmTTL() time.Duration {
	return time.Duration(p.ConfirmTTLMinutes) * time.Minute
}

func (p ReleasePolicy) ExecuteTimeout() time.Duration {
	return time.Duration(p.ExecuteTimeoutMinutes) * time.Minute
}

// MinConfirmReadSeconds is a product boundary, not a setting.
const MinConfirmReadSeconds = 10

// ActiveFreezes returns the freezes in effect at t.
func (p ReleasePolicy) ActiveFreezes(t time.Time) []Freeze {
	out := []Freeze{}
	for _, f := range p.Freezes {
		if !t.Before(f.StartsAt) && t.Before(f.EndsAt) {
			out = append(out, f)
		}
	}
	return out
}

type Announcement struct {
	Enabled bool   `json:"enabled"`
	Level   string `json:"level"` // info / warning
	Text    string `json:"text"`
}

type System struct {
	SiteName string `json:"siteName"`
	// BaseURL is the externally visible Tide URL, used in notification links.
	BaseURL      string       `json:"baseUrl"`
	Announcement Announcement `json:"announcement"`
}

func DefaultSystem() System {
	return System{SiteName: "Tide", Announcement: Announcement{Level: "info"}}
}

const (
	SectionOIDC         = "oidc"
	SectionUpstreams    = "upstreams"
	SectionEnvironments = "environments"
	SectionSecurity     = "security"
	SectionRelease      = "release"
	SectionCatalog      = "catalog"
	SectionNotify       = "notify"
	SectionSystem       = "system"
	SectionSetup        = "setup"
	SectionPipelineRepo = "pipeline"
)

var ErrNotConfigured = errors.New("not configured")

// Masked is what the API returns in place of a stored secret, and what a
// client sends back to mean "keep the current value".
const Masked = "••••••"

type Store struct {
	PG  *pg.Store
	Box *crypto.Box

	mu sync.RWMutex
	// cache holds decoded sections, and gen the cache_generation counter they
	// were read at. Another replica saving settings bumps that counter in the
	// same transaction, so the next read here sees a newer number and starts
	// over. Without it this cache had no expiry at all: a replica that missed
	// a change kept the old settings until the process restarted, which meant
	// a change freeze or a permission change could go unenforced on one pod.
	cache map[string]json.RawMessage
	gen   int64
}

// Invalidate drops this replica's copy. It is the local half of a change;
// the other replicas are reached through the generation counter.
func (s *Store) Invalidate() {
	s.mu.Lock()
	s.cache, s.gen = nil, 0
	s.mu.Unlock()
}

// Load decrypts section into v (a pointer). ErrNotConfigured when absent.
func (s *Store) Load(ctx context.Context, section string, v any) error {
	gen, err := s.PG.Cache.Generation(ctx, pg.ScopeSettings)
	if err != nil {
		return err
	}
	s.mu.RLock()
	raw, ok := s.cache[section]
	stale := s.gen != gen
	s.mu.RUnlock()
	if !ok || stale {
		b, err := s.PG.Settings.Get(ctx, section)
		if err != nil {
			return err
		}
		if b == nil {
			return ErrNotConfigured
		}
		raw = b
		s.mu.Lock()
		if s.gen != gen {
			// Everything cached describes an older world, not just this section.
			s.cache, s.gen = map[string]json.RawMessage{}, gen
		}
		if s.cache == nil {
			s.cache = map[string]json.RawMessage{}
		}
		s.cache[section] = raw
		s.mu.Unlock()
	}
	return s.decode(raw, v)
}

func (s *Store) decode(raw []byte, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return err
	}
	return walkSecrets(reflect.ValueOf(v), s.Box.Decrypt)
}

// Save stores next (a pointer) inside tx and audits the change. It reads the
// current value under a row lock, keeps secrets the client sent back as
// Masked or empty, encrypts secrets, and records before/after with secrets
// redacted.
func (s *Store) Save(ctx context.Context, tx *pg.Store, actor audit.Actor, section string, next any) error {
	prevRaw, err := tx.Settings.GetForUpdate(ctx, section)
	if err != nil {
		return err
	}
	var prev any
	if prevRaw != nil {
		prev = reflect.New(reflect.TypeOf(next).Elem()).Interface()
		if err := s.decode(prevRaw, prev); err != nil {
			return err
		}
		keepSecrets(reflect.ValueOf(next), reflect.ValueOf(prev))
	}
	changed := diffSecrets(reflect.ValueOf(next), reflect.ValueOf(prev), "")
	enc := deepCopy(next)
	if err := walkSecrets(reflect.ValueOf(enc), s.Box.Encrypt); err != nil {
		return err
	}
	b, err := json.Marshal(enc)
	if err != nil {
		return err
	}
	if err := tx.Settings.Put(ctx, section, b, actor.Sub); err != nil {
		return err
	}
	// Inside the caller's transaction: the counter and the row commit
	// together, so no replica can read one without the other.
	if err := tx.Cache.Bump(ctx, pg.ScopeSettings); err != nil {
		return err
	}
	s.Invalidate()
	var before map[string]any
	if prev != nil {
		before = Redacted(prev)
	}
	return tx.Audit.Write(ctx, actor, "settings.update", section, "", map[string]any{
		"before": before, "after": Redacted(next), "secretsChanged": changed,
	})
}

// Redacted returns v as a generic map with every secret masked.
func Redacted(v any) map[string]any {
	cp := deepCopy(v)
	_ = walkSecrets(reflect.ValueOf(cp), func(s string) (string, error) {
		if s == "" {
			return "", nil
		}
		return Masked, nil
	})
	b, _ := json.Marshal(cp)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func deepCopy(v any) any {
	b, _ := json.Marshal(v)
	cp := reflect.New(reflect.TypeOf(v).Elem()).Interface()
	_ = json.Unmarshal(b, cp)
	return cp
}

func walkSecrets(v reflect.Value, fn func(string) (string, error)) error {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return walkSecrets(v.Elem(), fn)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if t.Field(i).Tag.Get("secret") == "true" && f.Kind() == reflect.String {
				out, err := fn(f.String())
				if err != nil {
					return err
				}
				f.SetString(out)
				continue
			}
			if err := walkSecrets(f, fn); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if err := walkSecrets(v.Index(i), fn); err != nil {
				return err
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(v.MapIndex(k))
			if err := walkSecrets(elem, fn); err != nil {
				return err
			}
			v.SetMapIndex(k, elem)
		}
	}
	return nil
}

// Unmask fills in the secrets a caller left masked or empty, from what is
// already stored.
//
// Save does this too, but a caller that wants to *use* a secret before saving
// — to check the credentials actually work, say — needs it earlier: the mask
// is not a password, and checking with it fails as surely as a wrong one.
func (s *Store) Unmask(ctx context.Context, section string, next any) error {
	prev := reflect.New(reflect.TypeOf(next).Elem()).Interface()
	if err := s.Load(ctx, section, prev); err != nil {
		// Nothing stored yet: there is nothing to fill in, and whatever the
		// caller supplied is all there is.
		if errors.Is(err, ErrNotConfigured) {
			return nil
		}
		return err
	}
	keepSecrets(reflect.ValueOf(next), reflect.ValueOf(prev))
	return nil
}

// keepSecrets copies secrets from prev where next has Masked or "". Slices are
// matched by a Name field when present, otherwise by index.
func keepSecrets(next, prev reflect.Value) {
	for next.Kind() == reflect.Pointer {
		if next.IsNil() || prev.Kind() != reflect.Pointer || prev.IsNil() {
			return
		}
		next, prev = next.Elem(), prev.Elem()
	}
	switch next.Kind() {
	case reflect.Struct:
		t := next.Type()
		for i := 0; i < next.NumField(); i++ {
			f := next.Field(i)
			if t.Field(i).Tag.Get("secret") == "true" && f.Kind() == reflect.String {
				if f.String() == Masked || f.String() == "" {
					f.SetString(prev.Field(i).String())
				}
				continue
			}
			keepSecrets(f, prev.Field(i))
		}
	case reflect.Slice:
		for i := 0; i < next.Len(); i++ {
			n := next.Index(i)
			if p, ok := matchElem(n, prev); ok {
				keepSecrets(n, p)
			}
		}
	}
}

func matchElem(n reflect.Value, prev reflect.Value) (reflect.Value, bool) {
	if n.Kind() == reflect.Struct {
		if nf := n.FieldByName("Name"); nf.IsValid() {
			for j := 0; j < prev.Len(); j++ {
				if prev.Index(j).FieldByName("Name").String() == nf.String() {
					return prev.Index(j), true
				}
			}
			return reflect.Value{}, false
		}
	}
	return reflect.Value{}, false
}

func diffSecrets(next, prev reflect.Value, path string) []string {
	var out []string
	for next.Kind() == reflect.Pointer {
		if next.IsNil() {
			return nil
		}
		next = next.Elem()
		if prev.IsValid() && prev.Kind() == reflect.Pointer {
			if prev.IsNil() {
				prev = reflect.Value{}
			} else {
				prev = prev.Elem()
			}
		}
	}
	switch next.Kind() {
	case reflect.Struct:
		t := next.Type()
		for i := 0; i < next.NumField(); i++ {
			name := t.Field(i).Tag.Get("json")
			name, _, _ = strings.Cut(name, ",")
			var pf reflect.Value
			if prev.IsValid() {
				pf = prev.Field(i)
			}
			if t.Field(i).Tag.Get("secret") == "true" {
				old := ""
				if pf.IsValid() {
					old = pf.String()
				}
				if next.Field(i).String() != old {
					out = append(out, path+name)
				}
				continue
			}
			out = append(out, diffSecrets(next.Field(i), pf, path+name+".")...)
		}
	case reflect.Slice:
		for i := 0; i < next.Len(); i++ {
			n := next.Index(i)
			p := reflect.Value{}
			if prev.IsValid() {
				if m, ok := matchElem(n, prev); ok {
					p = m
				}
			}
			label := path
			if n.Kind() == reflect.Struct && n.FieldByName("Name").IsValid() {
				label += n.FieldByName("Name").String() + "."
			}
			out = append(out, diffSecrets(n, p, label)...)
		}
	}
	return out
}

// ApprovalPolicy is an approval rule and the environments it applies to.
type ApprovalPolicy struct {
	Envs []string `json:"envs"` // environment selectors
	// Projects and Types narrow the rule to services of these projects /
	// batch-dimension values; empty or "*" means all.
	Projects []string `json:"projects"`
	Types    []string `json:"types"`
	release.ApprovalRule
}

// specificity ranks matching rules: a project beats a type beats neither.
func (a ApprovalPolicy) specificity() int {
	n := 0
	if !rbac.ScopeMatches(a.Projects, "") {
		n += 2
	}
	if !rbac.ScopeMatches(a.Types, "") {
		n++
	}
	return n
}

// ApprovalFor returns the rule for a service of project / type in an
// environment, or nil when its releases need no approval. The most specific
// matching rule wins; among equally specific ones, the first.
func (p ReleasePolicy) ApprovalFor(env, tier, project, typ string) *release.ApprovalRule {
	best, score := -1, -1
	for i, a := range p.Approvals {
		if rbac.EnvMatches(a.Envs, env, tier) && rbac.ScopeMatches(a.Projects, project) && rbac.ScopeMatches(a.Types, typ) && a.specificity() > score {
			best, score = i, a.specificity()
		}
	}
	if best < 0 {
		return nil
	}
	r := p.Approvals[best].ApprovalRule
	return &r
}
