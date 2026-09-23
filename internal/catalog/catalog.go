// Package catalog builds the service × environment view from upstreams on
// demand. Tide does not store a service list: it would go stale. Reads are
// cached briefly so opening a page does not fan out to every upstream.
package catalog

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"tide/internal/cache"
	"tide/internal/i18n"
	"tide/internal/metrics"
	"tide/internal/settings"
	"tide/internal/store/pg"
	"tide/internal/upstream/argocd"
	"tide/internal/upstream/kargo"
	"tide/internal/upstream/registry"
)

// cacheTTL is how long a snapshot is served without anybody going back to the
// upstreams. It bounds how stale a change made outside Tide — somebody
// editing an Application, Argo CD syncing on its own — can be; changes made
// through Tide do not wait for it, because they move the generation counter
// and that invalidates the snapshot at once.
const cacheTTL = time.Minute

// staleLimit is how far past cacheTTL a snapshot may still be served while a
// rebuild runs behind it. Readers never wait inside this window; past it they
// do.
const staleLimit = 10 * time.Minute

// refreshEvery bounds how often a background rebuild may start.
const refreshEvery = 5 * time.Second

// sharedTTL only bounds how long a superseded snapshot lingers; a live one
// is replaced by a new key, not by expiry.
const sharedTTL = 10 * time.Minute

// Annotation Kargo requires on Applications it may update; it tells us the
// project and stage behind each Application.
const authorizedStageAnnotation = "kargo.akuity.io/authorized-stage"

type Clients struct {
	Name     string
	Envs     []string
	Kargo    *kargo.Client
	ArgoCD   *argocd.Client
	Registry *registry.Client
	Grafana  string
	// Expiry is when this upstream's credentials were said to stop working.
	// Dates only — the tokens stay in the clients that use them.
	Expiry settings.CredentialDates
}

type Deployment struct {
	Service       string     `json:"service"`
	Env           string     `json:"env"`
	Domain        string     `json:"domain"`
	Project       string     `json:"project,omitempty"`
	Upstream      string     `json:"upstream"`
	App           string     `json:"app"`
	Namespace     string     `json:"namespace"`
	Sync          string     `json:"sync"`
	Health        string     `json:"health"`
	HealthMessage string     `json:"healthMessage,omitempty"`
	Operation     string     `json:"operation,omitempty"`
	Images        []string   `json:"images"`
	Image         string     `json:"image,omitempty"` // repository, no tag
	Tag           string     `json:"tag,omitempty"`
	Digest        string     `json:"digest,omitempty"`
	Version       string     `json:"version,omitempty"`
	BuiltAt       *time.Time `json:"builtAt,omitempty"`
	KargoProject  string     `json:"kargoProject,omitempty"`
	KargoStage    string     `json:"kargoStage,omitempty"`
	Freight       string     `json:"freight,omitempty"`
	Since         *time.Time `json:"since,omitempty"`
	Promoting     string     `json:"promoting,omitempty"` // current Kargo promotion name
	AutoPromotion bool       `json:"autoPromotion"`
	AutoHeld      bool       `json:"autoHeld"`
	// Where this environment's manifests live, read from the Argo CD
	// Application. The Kargo pipeline generator writes the image tag here, so
	// it needs the exact repository, path and branch rather than a convention.
	Repo     string `json:"repo,omitempty"`
	RepoPath string `json:"repoPath,omitempty"`
	Revision string `json:"revision,omitempty"`
	// Workload says the Application deploys something that runs containers.
	// False for the ones that only set a namespace up: they have no image to
	// subscribe to and nothing to promote.
	Workload bool `json:"workload"`
	// ImageUnknown says the registry could not be asked about this image, so
	// the version and build time below are blank for want of an answer rather
	// than because nobody labelled the image. Without it the two look
	// identical on the page, and the row reads as a service with a sparse
	// build — not as a tag that names nothing in the registry, which is what
	// a placeholder left in git actually is.
	ImageUnknown bool   `json:"imageUnknown,omitempty"`
	Grafana      string `json:"grafana,omitempty"`
}

type Service struct {
	Name string `json:"name"`
	// Project groups domains; empty when neither a project label nor a Kargo
	// project applies.
	Project string `json:"project"`
	Domain  string `json:"domain"`
	// Dimensions holds the value of each configured catalog dimension, by key.
	Dimensions map[string]string      `json:"dimensions"`
	Envs       map[string]*Deployment `json:"envs"`
	// Conflicts explains why the name is ambiguous: the same service name in
	// more than one project, or more than one Application for one environment.
	// Tide shows such a service but refuses changes to it.
	Conflicts []string `json:"conflicts,omitempty"`
}

type UpstreamStatus struct {
	Name          string       `json:"name"`
	Envs          []string     `json:"envs"`
	KargoOK       bool         `json:"kargoOk"`
	KargoError    string       `json:"kargoError,omitempty"`
	KargoVersion  string       `json:"kargoVersion,omitempty"`
	ArgoCDOK      bool         `json:"argocdOk"`
	ArgoCDError   string       `json:"argocdError,omitempty"`
	ArgoCDVersion string       `json:"argocdVersion,omitempty"`
	Catalog       CatalogStats `json:"catalog"`
	// Expiring credentials, soonest first. Empty is the normal case: either
	// nothing is near its date, or nobody recorded one.
	Expiring []settings.CredentialExpiry `json:"expiring,omitempty"`
	// How image metadata lookups went. Failing them is not fatal — versions,
	// build times and image sizes simply go blank — which is exactly why it
	// has to be reported: silently blank reads as "nobody labelled these
	// images", while the usual cause is a credential that stopped working.
	RegistryFailed int    `json:"registryFailed,omitempty"`
	RegistryError  string `json:"registryError,omitempty"`
	// RegistryAuth says the failures were refusals rather than unreachability:
	// a new credential fixes them, waiting for the network will not.
	RegistryAuth bool      `json:"registryAuth,omitempty"`
	CheckedAt    time.Time `json:"checkedAt"`
}

// CatalogStats accounts for every Application the upstream returned. Without
// it, a label that matches nothing is indistinguishable from an upstream that
// holds nothing: each one drops every Application, and the page says "no
// services yet" — which about a few hundred Applications is simply false, and
// sends whoever reads it looking in the wrong place.
type CatalogStats struct {
	// Applications is what Argo CD returned, before any of it was classified.
	Applications int `json:"applications"`
	// Kept became deployments.
	Kept int `json:"kept"`
	// NoEnv is the interesting one: the environment dimension resolved to
	// nothing, which almost always means the configured label is not on the
	// Applications. NoEnv == Applications is a misconfiguration, not an
	// empty upstream.
	NoEnv int `json:"noEnv"`
	// OtherEnv resolved to an environment this upstream does not serve.
	// Ordinary — platform and infrastructure Applications land here.
	OtherEnv int `json:"otherEnv"`
	// NoWorkload resolved to an environment but runs nothing that can be
	// released. A Kustomize layer that only creates a Namespace, a set of
	// quotas or RBAC is a real Application, correctly labelled, and still not
	// a service: it has no image, so every column Tide shows about it is
	// empty and nothing can ever be promoted to it.
	NoWorkload int `json:"noWorkload"`
}

type Snapshot struct {
	// Labels counts label values across the Applications Tide maps, to help
	// administrators choose catalog labels. Not part of the services API.
	Labels    map[string]map[string]int `json:"-"`
	Services  []Service                 `json:"services"`
	Upstreams []UpstreamStatus          `json:"upstreams"`
	EnvOrder  []string                  `json:"envOrder"`
	At        time.Time                 `json:"at"`
}

type Hub struct {
	Settings *settings.Store
	PG       *pg.Store
	// Shared is the tier replicas have in common. Nil behaves like a cache
	// that always misses, which is the behaviour before there was one.
	Shared cache.Cache

	mu       sync.Mutex
	clients  map[string]*Clients
	envOrder []string
	catalog  settings.Catalog
	snap     *Snapshot
	// refreshed is when a background rebuild last started; see refreshEvery.
	refreshed time.Time
	// clientGen is the generation the clients were built at; gen the one the
	// snapshot was built at. They move together but are cached separately.
	clientGen int64
	// gen is the cache_generation this snapshot was built at. A release
	// finishing on another replica bumps it, so this one stops serving a view
	// that says the old version is still deployed.
	gen      int64
	building chan struct{}

	imgMu  sync.Mutex
	images map[string]*registry.Image // digest → metadata; digests are immutable
	// tags resolves "repo:tag" to a digest so a rebuild can reach the cache
	// above. Argo CD reports what is running as a tag, not a digest, so
	// without this every rebuild asked the registry about every deployment
	// again — the digest cache was never consulted on the path that matters.
	tags map[string]tagHit
}

// tagHit is a tag's digest and when it was learned. Tags, unlike digests, can
// be moved, so this expires; the digest it points at never does.
type tagHit struct {
	digest string
	at     time.Time
	// err is set when the lookup failed. Remembering the failure matters as
	// much as remembering success: an image that was never pushed fails on
	// every rebuild, and a few dozen of those are a few dozen round trips to
	// a registry across a site boundary.
	err error
}

// tagTTL bounds how stale a tag→digest answer can be. The tag itself always
// comes fresh from Argo CD; only the metadata behind it is cached, so the
// worst case is a version label and a digest that lag a few minutes behind a
// tag that was moved — and tags in a release pipeline are not moved.
const tagTTL = 5 * time.Minute

var ErrNoUpstreams = errors.New("no upstreams configured")

// Reset drops this replica's clients and snapshot. Callers that changed
// something other replicas must see bump ScopeCatalog in their transaction;
// this only handles the replica doing the work.
func (h *Hub) Reset() {
	h.mu.Lock()
	h.clients, h.snap, h.gen, h.clientGen = nil, nil, 0, 0
	h.mu.Unlock()
}

// load returns the upstream clients, rebuilding them when the catalog
// generation has moved. The clients carry addresses and tokens read from
// settings: without the generation check, an address or token changed on
// another replica would never reach this one, which is the same failure the
// settings cache had.
func (h *Hub) load(ctx context.Context, gen int64) (map[string]*Clients, []string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients != nil && h.clientGen == gen {
		return h.clients, h.envOrder, nil
	}
	var ups settings.Upstreams
	if err := h.Settings.Load(ctx, settings.SectionUpstreams, &ups); err != nil {
		if errors.Is(err, settings.ErrNotConfigured) {
			return nil, nil, ErrNoUpstreams
		}
		return nil, nil, err
	}
	envs, err := h.Settings.Environments(ctx)
	if err != nil {
		return nil, nil, err
	}
	if h.catalog, err = h.Settings.Catalog(ctx); err != nil {
		return nil, nil, err
	}
	h.clients = map[string]*Clients{}
	for _, u := range ups.Items {
		var served []string
		for _, e := range envs.Items {
			if e.Upstream == u.Name {
				served = append(served, e.Name)
			}
		}
		h.clients[u.Name] = Build(u, served)
	}
	h.envOrder, h.clientGen = envs.Order(), gen
	return h.clients, h.envOrder, nil
}

// Build creates clients for one upstream serving envs.
func Build(u settings.Upstream, envs []string) *Clients {
	return &Clients{
		Name: u.Name, Envs: envs, Grafana: u.GrafanaURL, Expiry: u.CredentialDates(),
		Kargo:    kargo.New(u.KargoURL, u.KargoToken, u.InsecureTLS),
		ArgoCD:   argocd.New(u.ArgoCDURL, u.ArgoCDToken, u.InsecureTLS),
		Registry: registry.New(u.RegistryURL, u.RegistryUser, u.RegistryToken, u.InsecureTLS),
	}
}

// ClientsFor returns the upstream serving env.
func (h *Hub) ClientsFor(ctx context.Context, env string) (*Clients, error) {
	gen, err := h.generation(ctx)
	if err != nil {
		return nil, err
	}
	cs, _, err := h.load(ctx, gen)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		if slices.Contains(c.Envs, env) {
			return c, nil
		}
	}
	return nil, fmt.Errorf("no upstream serves env %q", env)
}

func (h *Hub) Named(ctx context.Context, name string) (*Clients, error) {
	gen, err := h.generation(ctx)
	if err != nil {
		return nil, err
	}
	cs, _, err := h.load(ctx, gen)
	if err != nil {
		return nil, err
	}
	c, ok := cs[name]
	if !ok {
		return nil, fmt.Errorf("unknown upstream %q", name)
	}
	return c, nil
}

// Snapshot returns the cached view, rebuilding it when older than cacheTTL.
// Concurrent callers share one rebuild.
// Recent returns the last snapshot built, if it is no older than age, and
// never builds one.
//
// For callers that need a fact about a service which does not move — which
// project it belongs to, which type — rather than its live state. Snapshot
// would make them wait out a fan-out across every upstream whenever the
// thirty-second cache happened to have just expired, and one of those callers
// is the confirm button: pressing it went and rebuilt the catalogue before
// the release could start. A snapshot minutes old answers "which project is
// this service in" exactly as well as a fresh one.
//
// Nil means there is nothing recent enough, and the caller should fall back
// to Snapshot: an answer is still needed, it just no longer has to be free.
func (h *Hub) Recent(age time.Duration) *Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.snap == nil || time.Since(h.snap.At) > age {
		return nil
	}
	return h.snap
}

// Snapshot answers from the cache whenever it can, and never makes a reader
// wait for a rebuild it did not have to wait for.
//
// A reader that finds the snapshot out of date is handed the one there is and
// a rebuild starts behind it. That matters because the cache is invalidated
// by a counter every release bumps, and the counter is one counter for the
// whole catalog: twenty people releasing twenty different services invalidate
// it twenty times, each time for everybody. Making readers wait on that turns
// one person's release into everybody's slow page — and, because rebuilding
// takes longer than the gaps between releases during a busy hour, into a
// rebuild that never finishes catching up while every read queues behind it.
//
// fresh asks for a snapshot built now and waits for it, for the few callers
// that must see their own write. A snapshot older than staleLimit is not
// served at all: past that it is no longer "slightly behind", and a caller is
// better off waiting than being misled.
func (h *Hub) Snapshot(ctx context.Context, fresh bool) (*Snapshot, error) {
	gen, err := h.generation(ctx)
	if err != nil {
		return nil, err
	}
	if !fresh {
		h.mu.Lock()
		s, snapGen := h.snap, h.gen
		h.mu.Unlock()
		switch decide(s, snapGen, gen, time.Now()) {
		case serveCached:
			return s, nil
		case serveStale:
			// No context on purpose: the rebuild outlives the request that
			// noticed the staleness, and the reader is not waiting for it.
			h.refresh() //nolint:contextcheck // detached by design; see refresh
			return s, nil
		}
	}
	return h.rebuild(ctx, gen, fresh)
}

// What a reader gets.
type decision int

const (
	// serveCached: up to date, nobody rebuilds.
	serveCached decision = iota
	// serveStale: out of date but close enough to hand over while a rebuild
	// runs behind it.
	serveStale
	// mustBuild: nothing usable; the reader waits for a build.
	mustBuild
)

// decide is the whole staleness policy, kept in one place and out of the
// locking so it can be read and tested on its own.
func decide(s *Snapshot, snapGen, gen int64, now time.Time) decision {
	if s == nil {
		return mustBuild
	}
	age := now.Sub(s.At)
	switch {
	case snapGen == gen && age < cacheTTL:
		return serveCached
	case age < staleLimit:
		return serveStale
	default:
		return mustBuild
	}
}

// refresh starts a rebuild behind the readers being served stale data, at
// most one at a time and not more often than refreshEvery.
//
// The interval is what keeps a burst of releases from becoming a burst of
// fan-outs across every upstream: the counter may move forty times in a
// minute, and this still reads them once every few seconds.
func (h *Hub) refresh() {
	if !h.claimRefresh() {
		return
	}
	go func() {
		// Detached: this outlives the request that noticed the staleness.
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		gen, err := h.generation(ctx)
		if err != nil {
			zap.L().Warn("catalog: background refresh could not read the generation", zap.Error(err))
			return
		}
		if _, err := h.rebuild(ctx, gen, false); err != nil {
			zap.L().Warn("catalog: background refresh failed", zap.Error(err))
		}
	}()
}

// claimRefresh answers whether this caller is the one that starts the next
// background rebuild: not while one is already running, and not more often
// than refreshEvery. Separate from refresh so the rule can be tested without
// starting anything.
func (h *Hub) claimRefresh() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.building != nil || time.Since(h.refreshed) < refreshEvery {
		return false
	}
	h.refreshed = time.Now()
	return true
}

func (h *Hub) rebuild(ctx context.Context, gen int64, fresh bool) (*Snapshot, error) {
	for {
		h.mu.Lock()
		if h.snap != nil && !fresh && h.gen == gen && time.Since(h.snap.At) < cacheTTL {
			s := h.snap
			h.mu.Unlock()
			return s, nil
		}
		if h.building != nil {
			ch := h.building
			h.mu.Unlock()
			select {
			case <-ch:
				fresh = false
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		ch := make(chan struct{})
		h.building = ch
		h.mu.Unlock()

		// Another replica may have built this generation already. Reading its
		// bytes costs a round trip instead of a walk over every Application,
		// and both replicas then answer from the same snapshot.
		var err error
		s := h.fromShared(ctx, gen)
		if s == nil {
			// Detached from the request on purpose: other waiters are blocked
			// on this build, so the first caller navigating away must not
			// cancel it.
			bctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			buildStart := time.Now()
			s, err = h.build(bctx, gen) //nolint:contextcheck // see above: shared build outlives one request
			metrics.CatalogRefresh(time.Since(buildStart), err)
			if err == nil {
				// Before cancel: publishing with a cancelled context writes
				// nothing, and the failure would be swallowed as a degraded
				// cache, leaving every replica to rebuild for itself.
				h.toShared(bctx, gen, s) //nolint:contextcheck // same detached context
			}
			cancel()
		}
		h.mu.Lock()
		if err == nil {
			h.snap, h.gen = s, gen
		}
		h.building = nil
		close(ch)
		h.mu.Unlock()
		return s, err
	}
}

// snapshotKey stamps the generation into the key, so a change committed in
// PostgreSQL moves every replica to a different key. Nothing has to be
// deleted, and no invalidation message can go missing.
func snapshotKey(gen int64) string { return fmt.Sprintf("tide:catalog:snapshot:%d", gen) }

// wire is what goes into the shared cache. Snapshot.Labels is excluded from
// the API response (json:"-") but the label discovery page needs it, so it
// travels alongside rather than being silently dropped.
type wire struct {
	Snapshot *Snapshot                 `json:"snapshot"`
	Labels   map[string]map[string]int `json:"labels"`
}

// fromShared returns another replica's snapshot for this generation, or nil
// to mean "build it yourself". There is no error to return: every failure,
// including an unreadable value, is simply a miss.
func (h *Hub) fromShared(ctx context.Context, gen int64) *Snapshot {
	if h.Shared == nil {
		return nil
	}
	b, ok := h.Shared.Get(ctx, snapshotKey(gen))
	if !ok {
		return nil
	}
	var w wire
	if err := json.Unmarshal(b, &w); err != nil || w.Snapshot == nil {
		zap.L().Warn("discarding unreadable cached catalog", zap.Error(err))
		return nil
	}
	w.Snapshot.Labels = w.Labels
	return w.Snapshot
}

// toShared publishes a snapshot for the other replicas. The TTL is a floor
// under garbage collection, not the invalidation mechanism: superseded
// generations are already unreachable by key.
func (h *Hub) toShared(ctx context.Context, gen int64, s *Snapshot) {
	if h.Shared == nil || s == nil {
		return
	}
	b, err := json.Marshal(wire{Snapshot: s, Labels: s.Labels})
	if err != nil {
		zap.L().Warn("cannot cache catalog snapshot", zap.Error(err))
		return
	}
	h.Shared.Set(ctx, snapshotKey(gen), b, sharedTTL)
}

// generation reads the catalog counter, or 0 when the hub has no database
// (tests build a Hub directly). A constant 0 keeps the old TTL behaviour.
func (h *Hub) generation(ctx context.Context) (int64, error) {
	if h.PG == nil {
		return 0, nil
	}
	return h.PG.Cache.Generation(ctx, pg.ScopeCatalog)
}

func (h *Hub) build(ctx context.Context, gen int64) (*Snapshot, error) {
	cs, order, err := h.load(ctx, gen)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	cat := h.catalog
	h.mu.Unlock()
	snap := &Snapshot{EnvOrder: order, At: time.Now(), Labels: map[string]map[string]int{}}
	var mu sync.Mutex
	var deps []rawDeployment
	var tasks []func()
	for _, c := range cs {
		tasks = append(tasks, func() {
			st, ds := h.buildUpstream(ctx, c, cat)
			mu.Lock()
			defer mu.Unlock()
			snap.Upstreams = append(snap.Upstreams, st)
			deps = append(deps, ds...)
		})
	}
	parallel(len(tasks), tasks...)
	sort.Slice(snap.Upstreams, func(i, j int) bool { return snap.Upstreams[i].Name < snap.Upstreams[j].Name })
	snap.Services = mergeServices(deps)
	for _, d := range deps {
		for k, v := range d.labels {
			if snap.Labels[k] == nil {
				snap.Labels[k] = map[string]int{}
			}
			snap.Labels[k][v]++
		}
	}
	return snap, nil
}

// mergeServices groups deployments into services by name. Services are
// identified by name alone, so a name that maps to several projects, or to
// several Applications in one environment, is recorded as a conflict instead
// of one deployment silently replacing another. Input order does not matter.
func mergeServices(deps []rawDeployment) []Service {
	deps = slices.Clone(deps)
	slices.SortFunc(deps, func(a, b rawDeployment) int {
		return cmp.Or(strings.Compare(a.Service, b.Service), strings.Compare(a.Env, b.Env), strings.Compare(a.Upstream, b.Upstream), strings.Compare(a.App, b.App))
	})
	var out []Service
	for i := 0; i < len(deps); {
		j := i
		for j < len(deps) && deps[j].Service == deps[i].Service {
			j++
		}
		out = append(out, mergeService(deps[i:j]))
		i = j
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Domain != b.Domain {
			return a.Domain < b.Domain
		}
		return a.Name < b.Name
	})
	return out
}

// mergeService merges one service's deployments, sorted by env then app.
func mergeService(deps []rawDeployment) Service {
	svc := Service{Name: deps[0].Service, Domain: deps[0].domainHint, Dimensions: map[string]string{}, Envs: map[string]*Deployment{}}
	var projects []string
	apps := map[string][]string{} // env → "app (upstream)"
	var envs []string
	for i := range deps {
		d := &deps[i]
		if d.Project != "" && !slices.Contains(projects, d.Project) {
			projects = append(projects, d.Project)
		}
		for k, v := range d.dimensions {
			if svc.Dimensions[k] == "" {
				svc.Dimensions[k] = v
			}
		}
		if apps[d.Env] == nil {
			envs = append(envs, d.Env)
			svc.Envs[d.Env] = &d.Deployment
		}
		apps[d.Env] = append(apps[d.Env], fmt.Sprintf("%s（%s）", d.App, d.Upstream))
	}
	slices.Sort(projects)
	if len(projects) > 0 {
		svc.Project = projects[0]
	}
	if len(projects) > 1 {
		svc.Conflicts = append(svc.Conflicts, i18n.T(i18n.Default, "c.nameInManyProjects", svc.Name, strings.Join(projects, ", ")))
	}
	for _, e := range envs {
		if len(apps[e]) > 1 {
			svc.Conflicts = append(svc.Conflicts, i18n.T(i18n.Default, "c.manyApps", svc.Name, e, strings.Join(apps[e], ", ")))
		}
	}
	return svc
}

type rawDeployment struct {
	Deployment
	domainHint string
	dimensions map[string]string
	labels     map[string]string
}

// buildUpstream degrades per call: an unreachable Kargo or Argo CD is recorded
// in the status instead of failing the whole snapshot.
func (h *Hub) buildUpstream(ctx context.Context, c *Clients, cat settings.Catalog) (UpstreamStatus, []rawDeployment) {
	now := time.Now()
	st := UpstreamStatus{Name: c.Name, Envs: c.Envs, CheckedAt: now}
	// A credential that is about to expire breaks this upstream on a date
	// nobody is watching for. Saying so while it still works is the whole
	// point; once it has expired the only symptom is an upstream that stopped
	// answering, which reads as a network problem.
	st.Expiring = c.Expiry.Expiring(c.Name, now, settings.ExpiryWarnDays)
	for _, e := range st.Expiring {
		metrics.CredentialExpiry(e.Upstream, e.Kind, e.Days)
	}
	var apps []argocd.Application
	var stages = map[string]map[string]*kargo.Stage{} // project → stage → Stage
	// Timed in three parts, because "the catalog is slow" is not something
	// anybody can act on: listing Applications, walking the Kargo projects
	// and reading image metadata have completely different fixes, and which
	// one dominates decides which fix is worth writing.
	tListed := time.Now()
	parallel(2, func() {
		var err error
		if apps, err = c.ArgoCD.ListApplications(ctx); err != nil {
			st.ArgoCDError = err.Error()
			return
		}
		st.ArgoCDOK = true
		if v, err := c.ArgoCD.Version(ctx); err == nil {
			st.ArgoCDVersion = v
		}
	}, func() {
		v, err := c.Kargo.GetVersionInfo(ctx)
		if err != nil {
			st.KargoError = err.Error()
			return
		}
		st.KargoOK, st.KargoVersion = true, v
	})

	dListed := time.Since(tListed)
	deps, projects, stats := classify(apps, c, cat)
	st.Catalog = stats
	// Saying this once per build costs nothing and is the difference between
	// an hour of reading code and a glance at the log.
	// An upstream no environment references is meant to classify nothing, so
	// saying "everything was dropped" about it is noise — and a diagnostic
	// that cries wolf is one people learn to scroll past.
	if st.Catalog.Applications > 0 && st.Catalog.Kept == 0 && len(c.Envs) > 0 {
		zap.L().Warn("catalog: every application was dropped",
			zap.String("upstream", c.Name), zap.Int("applications", st.Catalog.Applications),
			zap.Int("no_env", st.Catalog.NoEnv), zap.Int("other_env", st.Catalog.OtherEnv),
			zap.String("env_label", cat.EnvLabel), zap.Strings("envs", c.Envs))
	}
	tStages := time.Now()
	if st.KargoOK {
		var mu sync.Mutex
		var tasks []func()
		for p := range projects {
			tasks = append(tasks, func() {
				list, err := c.Kargo.ListStages(ctx, p)
				if err != nil {
					zap.L().Warn("kargo list stages failed", zap.String("upstream", c.Name), zap.String("project", p), zap.Error(err))
					return
				}
				m := map[string]*kargo.Stage{}
				for i := range list {
					m[list[i].Metadata.Name] = &list[i]
				}
				mu.Lock()
				stages[p] = m
				mu.Unlock()
			})
		}
		parallel(8, tasks...)
	}
	dStages := time.Since(tStages)
	for i := range deps {
		d := &deps[i]
		if s := stages[d.KargoProject][d.KargoStage]; s != nil {
			applyStage(&d.Deployment, s)
		}
	}
	tImages := time.Now()
	st.RegistryFailed, st.RegistryError, st.RegistryAuth = h.fillVersions(ctx, c, deps)
	dImages := time.Since(tImages)
	zap.L().Info("catalog: upstream read",
		zap.String("upstream", c.Name),
		zap.Duration("applications", dListed), zap.Int("application_count", st.Catalog.Applications),
		zap.Duration("stages", dStages), zap.Int("project_count", len(projects)),
		zap.Duration("images", dImages), zap.Int("deployment_count", len(deps)),
		zap.Duration("total", time.Since(tListed)))
	if st.RegistryFailed > 0 {
		zap.L().Warn("catalog: image metadata unavailable",
			zap.String("upstream", c.Name), zap.Int("failed", st.RegistryFailed),
			zap.Bool("authentication", st.RegistryAuth), zap.String("error", st.RegistryError))
	}
	return st, deps
}

// classify turns the Applications an upstream returned into deployments, and
// accounts for the ones it did not. The accounting is the point: dropping an
// Application is silent by nature, and a whole catalog dropped silently looks
// exactly like an upstream with nothing in it.
func classify(apps []argocd.Application, c *Clients, cat settings.Catalog) ([]rawDeployment, map[string]bool, CatalogStats) {
	var deps []rawDeployment
	projects := map[string]bool{}
	stats := CatalogStats{Applications: len(apps)}
	for _, a := range apps {
		d, ok := fromApp(a, c, cat)
		if !ok {
			// fromApp leaves Env set on the way out, which is what separates
			// "the environment dimension resolved to nothing" from "it
			// resolved to an environment this upstream does not serve".
			if d.Env == "" {
				stats.NoEnv++
			} else {
				stats.OtherEnv++
			}
			continue
		}
		// Classified correctly and still not a service; see NoWorkload.
		if !d.Workload {
			stats.NoWorkload++
			continue
		}
		stats.Kept++
		if d.KargoProject != "" {
			projects[d.KargoProject] = true
		}
		deps = append(deps, d)
	}
	return deps, projects, stats
}

func fromApp(a argocd.Application, c *Clients, cat settings.Catalog) (rawDeployment, bool) {
	var d rawDeployment
	d.Upstream, d.App = c.Name, a.Metadata.Name
	d.Namespace = a.Spec.Destination.Namespace
	d.Sync, d.Health, d.HealthMessage = a.Status.Sync.Status, a.Status.Health.Status, a.Status.Health.Message
	if op := a.Status.OperationState; op != nil {
		d.Operation = op.Phase
	}
	d.Images = a.Status.Summary.Images
	d.Workload = a.RunsWorkloads()
	if src, ok := a.Manifests(); ok {
		d.Repo, d.RepoPath, d.Revision = src.RepoURL, src.Path, src.TargetRevision
	}
	// A template that rendered with an empty segment leaves something like
	// ":dev" or "-pipeline:dev" behind. Treating that as a real project name
	// produces a Kargo lookup that can only fail, and the service then reports
	// "not managed by Kargo" for the wrong reason. An annotation whose project
	// or stage is blank means the same as no annotation at all.
	if proj, stage, ok := strings.Cut(a.Metadata.Annotations[authorizedStageAnnotation], ":"); ok &&
		isKargoName(proj) && stage != "" {
		d.KargoProject, d.KargoStage = proj, stage
	}
	labels := a.Metadata.Labels
	d.labels = labels
	// The Argo CD project, split once and reused for whichever dimensions ask
	// for it.
	argoLine, argoEnv := argoProjectValue(a.Spec.Project, c.Envs)
	d.Env = labelValue(labels, cat.EnvLabel)
	if cat.EnvLabel == FromArgoProject {
		d.Env = argoEnv
	}
	if d.Env == "" && slices.Contains(c.Envs, d.KargoStage) {
		d.Env = d.KargoStage
	}
	if d.Env == "" {
		for _, e := range c.Envs {
			if strings.HasSuffix(a.Metadata.Name, "-"+e) {
				d.Env = e
			}
		}
	}
	// Not one of ours (e.g. platform apps). Env is deliberately left as it
	// was resolved: buildUpstream reads it to tell a label that matched
	// nothing from an environment that belongs to somebody else.
	if d.Env == "" || !slices.Contains(c.Envs, d.Env) {
		return d, false
	}
	d.Service = labelValue(labels, cat.ServiceLabel)
	if cat.ServiceLabel == FromArgoProject {
		d.Service = argoLine
	}
	if d.Service == "" {
		d.Service = strings.TrimSuffix(a.Metadata.Name, "-"+d.Env)
	}
	d.Domain = labelValue(labels, cat.DomainLabel)
	if cat.DomainLabel == FromArgoProject {
		d.Domain = argoLine
	}
	if d.Domain == "" {
		d.Domain = strings.TrimSuffix(d.Namespace, "-"+d.Env)
	}
	d.domainHint = d.Domain
	d.Project = labelValue(labels, cat.ProjectLabel)
	if cat.ProjectLabel == FromArgoProject {
		d.Project = argoLine
	}
	if d.Project == "" {
		// Falling back to the Kargo project is right where Kargo projects group
		// services, and wrong where each service has one — then every service
		// looks like its own project. FromArgoProject exists for that case.
		d.Project = d.KargoProject
	}
	d.dimensions = map[string]string{}
	for _, dim := range cat.Dimensions {
		if v := labelValue(labels, dim.Label); v != "" {
			d.dimensions[dim.Key] = v
		}
	}
	if len(d.Images) > 0 {
		d.Image, d.Tag = splitRef(d.Images[0])
	}
	if c.Grafana != "" {
		d.Grafana = strings.NewReplacer("{service}", d.Service, "{env}", d.Env, "{namespace}", d.Namespace).Replace(c.Grafana)
	}
	return d, true
}

// isKargoName rejects what a half-rendered template leaves behind: an empty
// string, or a name that is only the template's literal part ("-pipeline").
// A Kargo project is a Kubernetes namespace, so it starts and ends with an
// alphanumeric.
func isKargoName(s string) bool {
	if s == "" {
		return false
	}
	first, last := s[0], s[len(s)-1]
	ok := func(c byte) bool {
		return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
	}
	return ok(first) && ok(last)
}

func labelValue(labels map[string]string, key string) string {
	if key == "" {
		return ""
	}
	return labels[key]
}

// FromArgoProject asks for a dimension to be read from the Argo CD project
// instead of a label. Some setups already encode what Tide wants in the
// AppProject name — "acme-dev" is a business line and an environment — and
// making Tide read that is one setting, against labelling every Application.
const FromArgoProject = "argocd:project"

// argoProjectValue splits an Argo CD project name into its leading part and
// its environment suffix: "acme-dev" with envs [dev qa] gives ("acme", "dev").
// A name with no known environment suffix is returned whole, because that is
// what a project without a per-environment split looks like.
func argoProjectValue(project string, envs []string) (line, env string) {
	for _, e := range envs {
		if strings.HasSuffix(project, "-"+e) {
			return strings.TrimSuffix(project, "-"+e), e
		}
	}
	return project, ""
}

func applyStage(d *Deployment, s *kargo.Stage) {
	d.AutoPromotion = s.Status.AutoPromotionEnabled
	d.AutoHeld = len(s.Status.EffectiveAutoPromotionHolds) > 0
	if s.Status.CurrentPromotion != nil {
		d.Promoting = s.Status.CurrentPromotion.Name
	}
	cur := s.Current()
	if cur == nil {
		return
	}
	d.Freight = cur.Name
	for _, img := range cur.Images {
		if d.Image == "" || sameRepo(img.RepoURL, d.Image) {
			d.Image, d.Tag, d.Digest = img.RepoURL, img.Tag, img.Digest
			break
		}
	}
	if s.Status.LastPromotion != nil && s.Status.LastPromotion.FinishedAt != nil {
		d.Since = s.Status.LastPromotion.FinishedAt
	}
}

// fillVersions adds image metadata where the registry answers; misses leave
// the version empty rather than failing the view. It reports how many lookups
// failed, one error worth showing for them, and whether they were refusals.
//
// The count and the message are the point. Dropping these errors made an
// expired registry credential look like images nobody had labelled — no error
// anywhere, just emptier rows — which is the hardest kind of problem to go
// looking for.
func (h *Hub) fillVersions(ctx context.Context, c *Clients, deps []rawDeployment) (failed int, message string, auth bool) {
	var mu sync.Mutex
	var tasks []func()
	for i := range deps {
		d := &deps[i]
		if d.Image == "" || (d.Digest == "" && d.Tag == "") {
			continue
		}
		tasks = append(tasks, func() {
			ref := d.Digest
			if ref == "" {
				ref = d.Tag
			}
			img, err := h.Inspect(ctx, c, d.Image, ref)
			if err != nil {
				isAuth := errors.Is(err, registry.ErrUnauthorized)
				d.ImageUnknown = true
				mu.Lock()
				failed++
				// Authentication wins the reported message even when it is not
				// the first failure: it names something a person can go and
				// fix, where a timeout usually names a symptom of it.
				if message == "" || (isAuth && !auth) {
					message, auth = truncateError(err.Error()), isAuth
				}
				mu.Unlock()
				return
			}
			d.Digest, d.Version, d.BuiltAt = img.Digest, img.Version(), img.Created
		})
	}
	parallel(8, tasks...)
	return failed, message, auth
}

// truncateError keeps the upstream's own words but not all of them: registry
// bodies can be long, and this one ends up on a page.
func truncateError(s string) string {
	const limit = 300
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// Inspect reads image metadata, caching by digest.
// imageKey is not stamped with a generation: a digest names one immutable
// manifest, so what it says about an image never changes.
func imageKey(digest string) string { return "tide:catalog:image:" + digest }

// imageTTL is long because the answer cannot go stale, only unused.
const imageTTL = 24 * time.Hour

func (h *Hub) Inspect(ctx context.Context, c *Clients, image, ref string) (*registry.Image, error) {
	digest := ref
	byTag := !strings.HasPrefix(ref, "sha256:")
	if byTag {
		// A tag. Resolve it to a digest first, or the cache below — keyed by
		// digest, and correct forever — can never be reached.
		switch d, err, ok := h.tagged(image, ref); {
		case ok && err != nil:
			return nil, err
		case ok:
			digest = d
		default:
			// Nothing in this process. The digest behind the tag may still be
			// in the tier the replicas share, and that is what makes a
			// restart cheap: every image's metadata is already there, keyed
			// by digest, and only the tag → digest step was lost with the
			// process. Without it a fresh pod asks the registry about every
			// deployment again — measured at 10.8 of the 11.2 seconds a cold
			// build takes.
			digest = h.digestFromShared(ctx, image, ref)
			if digest != "" {
				h.rememberTag(image, ref, digest, nil)
			}
		}
	}
	if digest != "" {
		h.imgMu.Lock()
		img := h.images[digest]
		h.imgMu.Unlock()
		if img != nil {
			return img, nil
		}
		if img := h.imageFromShared(ctx, digest); img != nil {
			h.remember(img)
			return img, nil
		}
	}
	_, repo := registry.SplitImage(image)
	img, err := c.Registry.Inspect(ctx, repo, ref)
	if err != nil {
		h.rememberTag(image, ref, "", err)
		return nil, err
	}
	h.remember(img)
	h.rememberTag(image, ref, img.Digest, nil)
	if h.Shared != nil {
		if b, err := json.Marshal(img); err == nil {
			h.Shared.Set(ctx, imageKey(img.Digest), b, imageTTL)
		}
		// Only what resolved, and only for a tag. A failure stays in this
		// process: sharing it would hand every replica one replica's bad
		// minute, and the failures worth remembering are cheap to rediscover.
		if byTag {
			h.Shared.Set(ctx, tagKey(image, ref), []byte(img.Digest), tagTTL)
		}
	}
	return img, nil
}

// tagged returns what this tag resolved to last time, while that is still
// fresh enough to believe.
func (h *Hub) tagged(image, tag string) (digest string, err error, ok bool) {
	if tag == "" {
		return "", nil, false
	}
	h.imgMu.Lock()
	defer h.imgMu.Unlock()
	hit, ok := h.tags[image+":"+tag]
	if !ok || time.Since(hit.at) > tagTTL {
		return "", nil, false
	}
	return hit.digest, hit.err, true
}

func (h *Hub) rememberTag(image, tag, digest string, err error) {
	if tag == "" || strings.HasPrefix(tag, "sha256:") {
		return
	}
	h.imgMu.Lock()
	defer h.imgMu.Unlock()
	if h.tags == nil {
		h.tags = map[string]tagHit{}
	}
	h.tags[image+":"+tag] = tagHit{digest: digest, at: time.Now(), err: err}
}

func (h *Hub) remember(img *registry.Image) {
	h.imgMu.Lock()
	if h.images == nil {
		h.images = map[string]*registry.Image{}
	}
	h.images[img.Digest] = img
	h.imgMu.Unlock()
}

// tagKey is stamped with neither a generation nor a digest: it is the one
// mapping here that can change, which is why it expires with tagTTL rather
// than living as long as the metadata it points at.
func tagKey(image, tag string) string { return "tide:catalog:tag:" + image + ":" + tag }

func (h *Hub) digestFromShared(ctx context.Context, image, tag string) string {
	if h.Shared == nil {
		return ""
	}
	b, ok := h.Shared.Get(ctx, tagKey(image, tag))
	if !ok || !strings.HasPrefix(string(b), "sha256:") {
		return ""
	}
	return string(b)
}

func (h *Hub) imageFromShared(ctx context.Context, digest string) *registry.Image {
	if h.Shared == nil {
		return nil
	}
	b, ok := h.Shared.Get(ctx, imageKey(digest))
	if !ok {
		return nil
	}
	var img registry.Image
	if err := json.Unmarshal(b, &img); err != nil {
		return nil
	}
	return &img
}

func splitRef(ref string) (repo, tag string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, ""
}

func sameRepo(a, b string) bool {
	_, ra := registry.SplitImage(a)
	_, rb := registry.SplitImage(b)
	return ra == rb
}

// Find returns one service from the snapshot.
func (s *Snapshot) Find(name string) *Service {
	for i := range s.Services {
		if s.Services[i].Name == name {
			return &s.Services[i]
		}
	}
	return nil
}
