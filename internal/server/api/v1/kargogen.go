package v1

import (
	"archive/zip"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"tide/internal/catalog"
	"tide/internal/i18n"
	"tide/internal/kargogen"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
	"tide/internal/settings"
	"tide/internal/upstream/gitea"
	"tide/internal/upstream/gitlab"
	pusher "tide/internal/upstream/repo"
)

// generateKargo previews the Kargo pipeline for one business domain, or for
// all of them. It generates and returns text; nothing is written anywhere.
func (a *API) generateKargo(c *gin.Context) {
	p := newParams(c)
	domain := p.text("domain", 128)
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	res, snap, err := a.kargoPlan(c, domain)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, gin.H{
		"result":  res,
		"domains": domainsOf(snap),
		"at":      snap.At,
	})
}

// downloadKargo returns the same files as a zip. Not under the response
// envelope: it is a file, and a browser downloading it has nowhere to put a
// `code` field.
func (a *API) downloadKargo(c *gin.Context) {
	p := newParams(c)
	domain := p.text("domain", 128)
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	res, _, err := a.kargoPlan(c, domain)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	files := res.Files()
	if len(files) == 0 {
		respond.Fail(c, errcode.NewKey(errcode.NotFound, "kargogen.nothingToGenerate"))
		return
	}

	name := "kargo-pipeline"
	if domain != "" {
		name += "-" + domain
	}
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", `attachment; filename="`+name+`.zip"`)
	z := zip.NewWriter(c.Writer)
	for _, f := range files {
		// A fixed timestamp keeps the archive byte-identical between two runs
		// over the same catalog, so "did anything change" is answerable by
		// comparing files rather than by reading them.
		w, err := z.CreateHeader(&zip.FileHeader{Name: f.Path, Method: zip.Deflate,
			Modified: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			break
		}
		if _, err := w.Write([]byte(f.YAML)); err != nil {
			break
		}
	}
	_ = z.Close()
}

// kargoPlan runs the generator over the current catalog.
func (a *API) kargoPlan(c *gin.Context, domain string) (kargogen.Result, *catalog.Snapshot, error) {
	ctx := c.Request.Context()
	// Fresh: the generated pipeline describes what is deployed right now, and
	// a stale snapshot would silently leave out a service somebody added
	// minutes ago. This is not a page anyone opens repeatedly.
	snap, err := a.Hub.Snapshot(ctx, true)
	if err != nil {
		return kargogen.Result{}, nil, err
	}
	envs, err := a.Settings.Environments(ctx)
	if err != nil {
		return kargogen.Result{}, nil, err
	}
	cat, err := a.Settings.Catalog(ctx)
	if err != nil {
		return kargogen.Result{}, nil, err
	}
	// The labels Tide reads the catalog by are the labels the generated
	// stages should carry: a promotion policy selecting on them then means the
	// same thing as a filter on the services page.
	return kargogen.Generate(snap, envs, kargogen.Options{
		Domain:       domain,
		ServiceLabel: labelOrDefault(cat.ServiceLabel, "tide.io/service"),
		EnvLabel:     labelOrDefault(cat.EnvLabel, "tide.io/env"),
	}), snap, nil
}

// labelOrDefault ignores the pseudo-labels that mean "read this from the Argo
// CD project rather than from a label": they are not label keys and cannot be
// written onto a stage.
func labelOrDefault(key, fallback string) string {
	if key == "" || strings.Contains(key, ":") {
		return fallback
	}
	return key
}

// domainsOf lists the business domains in the catalog with how many services
// each holds, so the page can offer a choice without generating everything
// first.
func domainsOf(snap *catalog.Snapshot) []gin.H {
	count := map[string]int{}
	for _, s := range snap.Services {
		if s.Domain != "" {
			count[s.Domain]++
		}
	}
	names := make([]string, 0, len(count))
	for n := range count {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]gin.H, 0, len(names))
	for _, n := range names {
		out = append(out, gin.H{"name": n, "services": count[n]})
	}
	return out
}

// pushKargo commits the generated pipeline to the configured repository, in
// one commit. It writes text into git and nothing into a cluster: Argo CD
// applies it afterwards, which is what keeps the cluster and the repository
// telling the same story.
func (a *API) pushKargo(c *gin.Context) {
	var req struct {
		Domain string `json:"domain"`
		// Message overrides the commit message; empty takes the default.
		Message string `json:"message"`
	}
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx := c.Request.Context()

	var cfg settings.PipelineRepo
	if err := a.Settings.Load(ctx, settings.SectionPipelineRepo, &cfg); err != nil && !errors.Is(err, settings.ErrNotConfigured) {
		respond.Fail(c, err)
		return
	}
	if !cfg.Configured() {
		respond.Fail(c, errcode.NewKey(errcode.NoUpstreams, "kargogen.repoNotConfigured"))
		return
	}

	res, _, err := a.kargoPlan(c, strings.TrimSpace(req.Domain))
	if err != nil {
		respond.Fail(c, err)
		return
	}
	files := res.Files()
	if len(files) == 0 {
		respond.Fail(c, errcode.NewKey(errcode.NotFound, "kargogen.nothingToGenerate"))
		return
	}

	prefix := strings.Trim(cfg.PathPrefix, "/")
	out := make([]pusher.File, 0, len(files))
	for _, f := range files {
		path := f.Path
		if prefix != "" {
			path = prefix + "/" + path
		}
		out = append(out, pusher.File{Path: path, Content: f.YAML})
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = defaultCommitMessage(req.Domain, res)
	}
	commit, err := pushTo(cfg).Push(ctx, cfg.Branch, message, out)
	if err != nil {
		respond.Fail(c, err)
		return
	}

	_ = a.PG.Audit.Write(ctx, currentUser(c).Actor(), "kargo.push", req.Domain, "", map[string]any{
		"provider": cfg.Host(), "project": cfg.Project, "branch": cfg.Branch, "commit": commit.ID,
		"files": len(out), "stages": res.Stages, "warehouses": res.Warehouses,
	})
	respond.OK(c, gin.H{"commit": commit, "branch": cfg.Branch, "files": len(out), "result": res})
}

// defaultCommitMessage says what changed in the subject, because a repository
// full of "update pipeline" tells nobody anything six months later.
func defaultCommitMessage(domain string, res kargogen.Result) string {
	what := i18n.T(i18n.Default, "kargogen.commitAll")
	if domain != "" {
		what = domain
	}
	return i18n.T(i18n.Default, "kargogen.commitMessage", what, res.Warehouses, res.Stages)
}

// pushTo picks the client for the configured host.
func pushTo(cfg settings.PipelineRepo) pusher.Pusher {
	if cfg.Host() == settings.ProviderGitea {
		return gitea.New(cfg.BaseURL, cfg.Project, cfg.Token, false)
	}
	return gitlab.New(cfg.BaseURL, cfg.Project, cfg.Token, false)
}
