// Package kargogen turns Tide's service catalog into Kargo pipeline YAML:
// one Project per business domain, one Warehouse per service, one Stage per
// service and environment, and one shared promotion task per project.
//
// It generates text and nothing else. Applying it is git's job and Argo CD's
// job, which is the point: a pipeline written straight into the cluster by an
// API call is a pipeline nobody can review and nothing can roll back.
package kargogen

import (
	"fmt"
	"sort"
	"strings"

	"tide/internal/catalog"
	"tide/internal/settings"
)

// File is one generated document, named after what it holds.
type File struct {
	// Path is relative, "<domain>/<name>.yaml".
	Path string `json:"path"`
	YAML string `json:"yaml"`
}

// Skipped is a service that produced nothing, and why. Never silent: a
// generator that quietly drops a service leaves somebody with a release
// process that is missing exactly one thing, and no way to find out which.
type Skipped struct {
	Service string `json:"service"`
	Env     string `json:"env,omitempty"`
	Reason  string `json:"reason"`
}

// Domain is everything generated for one business domain, which is also one
// Kargo project. Grouped rather than flat because the domain is the unit a
// person reviews: a list of ninety files sorted by name is not reviewable.
type Domain struct {
	Name       string `json:"name"`
	Services   int    `json:"services"`
	Warehouses int    `json:"warehouses"`
	Stages     int    `json:"stages"`
	Files      []File `json:"files"`
}

// Result is what one run produced.
type Result struct {
	Domains []Domain  `json:"domains"`
	Skipped []Skipped `json:"skipped"`
	// Totals, so the page does not recompute what the generator already knows.
	Services   int `json:"services"`
	Warehouses int `json:"warehouses"`
	Stages     int `json:"stages"`
	FileCount  int `json:"fileCount"`
}

// Files flattens every domain's files, in generation order. The zip and the
// commit both want one list; the page wants them grouped.
func (r Result) Files() []File {
	out := make([]File, 0, r.FileCount)
	for _, d := range r.Domains {
		out = append(out, d.Files...)
	}
	return out
}

// Options are the few things not derivable from the catalog.
type Options struct {
	// Domain limits generation to one business domain; empty does all of them.
	Domain string
	// ImageStrategy and TagPattern decide which tag a warehouse treats as the
	// newest. Empty falls back to the defaults in withDefaults.
	ImageStrategy string
	TagPattern    string
	// ProjectPrefix goes in front of the domain to make the Kargo project's
	// name. A Kargo project is a cluster-scoped namespace, so a bare domain
	// like "base" collides the moment a second business line has one too;
	// prefixing with the line ("acme-base") matches how the deployment
	// namespaces are already named.
	ProjectPrefix bool
	// TaskName is the shared promotion task's name within each project.
	TaskName string
	// Labels put on every generated Stage, so promotion policies can select
	// them by service and environment. Keys are the label prefixes Tide uses
	// elsewhere.
	ServiceLabel string
	EnvLabel     string
}

// projectName is what the Kargo project is called: the domain, optionally
// behind the business line it belongs to.
func projectName(svc catalog.Service, prefix bool) string {
	if !prefix || svc.Project == "" {
		return svc.Domain
	}
	// An Application with no project label falls back to the Kargo project
	// named in its authorized-stage annotation, and that name already has the
	// domain in it. Appending the domain again invents a project nobody
	// asked for — "acme-shop-shop" — writes a directory for it and asks Kargo
	// to create it. The services would land somewhere plausible-looking and
	// entirely separate from the siblings they belong with.
	if svc.Project == svc.Domain || strings.HasSuffix(svc.Project, "-"+svc.Domain) {
		return svc.Project
	}
	return svc.Project + "-" + svc.Domain
}

func (o Options) withDefaults() Options {
	if o.TaskName == "" {
		o.TaskName = "promote-image"
	}
	if o.ServiceLabel == "" {
		o.ServiceLabel = "tide.io/service"
	}
	if o.EnvLabel == "" {
		o.EnvLabel = "tide.io/env"
	}
	if o.ImageStrategy == "" {
		o.ImageStrategy = settings.StrategyLexical
	}
	if o.TagPattern == "" {
		o.TagPattern = settings.DefaultTagPattern
	}
	return o
}

// Generate builds the YAML for everything in the snapshot that can have a
// pipeline. envOrder is the promotion order: the first environment a service
// is deployed to takes freight straight from its warehouse, and each later one
// takes it from the environment before it.
func Generate(snap *catalog.Snapshot, envs settings.Environments, opts Options) Result {
	opts = opts.withDefaults()
	order := envs.Order()
	// Empty, not nil: a nil slice marshals to `null`, and a caller that was
	// promised a list and handed null has to defend against it everywhere or
	// crash somewhere. The zero case is "no domains", not "no answer".
	res := Result{Domains: []Domain{}, Skipped: []Skipped{}}

	// Group by domain first: a domain becomes a Kargo project, and a project
	// is the unit everything else is written into.
	byDomain := map[string][]catalog.Service{}
	for _, svc := range snap.Services {
		if svc.Domain == "" {
			res.Skipped = append(res.Skipped, Skipped{Service: svc.Name, Reason: "no business domain"})
			continue
		}
		// Filter on the domain as the catalog spells it, not on the prefixed
		// project name: the picker offers domains.
		if opts.Domain != "" && svc.Domain != opts.Domain {
			continue
		}
		name := projectName(svc, opts.ProjectPrefix)
		byDomain[name] = append(byDomain[name], svc)
	}

	for _, name := range sortedKeys(byDomain) {
		files, skipped, counts := generateDomain(name, byDomain[name], order, opts)
		res.Skipped = append(res.Skipped, skipped...)
		if counts.services == 0 {
			continue
		}
		res.Domains = append(res.Domains, Domain{
			Name: name, Services: counts.services,
			Warehouses: counts.warehouses, Stages: counts.stages, Files: files,
		})
		res.Services += counts.services
		res.Warehouses += counts.warehouses
		res.Stages += counts.stages
		res.FileCount += len(files)
	}
	return res
}

type counts struct{ services, warehouses, stages int }

func generateDomain(domain string, services []catalog.Service, order []string, opts Options) ([]File, []Skipped, counts) {
	var skipped []Skipped
	var c counts
	var warehouses, stages []string

	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	for _, svc := range services {
		// The environments this service is actually deployed to, in promotion
		// order. Generating a stage for an environment with no Application
		// produces a pipeline pointing at a path that does not exist.
		var deployed []*catalog.Deployment
		for _, env := range order {
			d := svc.Envs[env]
			switch {
			case d == nil:
				continue
			case !d.Workload:
				skipped = append(skipped, Skipped{Service: svc.Name, Env: env,
					Reason: "deploys no workload, so there is no image to promote"})
				continue
			case d.Image == "":
				skipped = append(skipped, Skipped{Service: svc.Name, Env: env,
					Reason: "no image could be read from the Application"})
				continue
			case d.Repo == "" || d.RepoPath == "":
				skipped = append(skipped, Skipped{Service: svc.Name, Env: env,
					Reason: "the Application combines several sources, so there is no single place to write the tag"})
				continue
			}
			deployed = append(deployed, d)
		}
		if len(deployed) == 0 {
			continue
		}
		c.services++
		// Split the environments into artifact lines: consecutive ones that
		// pull from the same image repository. A promotion moves a tag, and
		// the tag has to exist in the repository the next environment pulls
		// from, so a change of repository is a wall — everything on the far
		// side of it can only take its own builds, from a warehouse of its
		// own. A project that builds once and promotes onward has exactly one
		// line and comes out of this unchanged.
		for _, ln := range lines(deployed) {
			// The first line keeps the service's own name so that pipelines
			// generated before there was more than one line are not renamed
			// out from under a cluster that is already running them.
			name := svc.Name
			if ln.first > 0 {
				name = svc.Name + "-" + ln.envs[0].Env
			}
			warehouses = append(warehouses, warehouseYAML(name, domain, ln.envs[0].Image, opts))
			c.warehouses++
			for i, d := range ln.envs {
				from := ""
				if i > 0 {
					from = stageName(svc.Name, ln.envs[i-1].Env)
				}
				stages = append(stages, stageYAML(svc.Name, name, domain, d, from, opts))
				c.stages++
			}
		}
	}
	if c.services == 0 {
		return nil, skipped, c
	}

	files := []File{
		{Path: domain + "/project.yaml", YAML: projectYAML(domain)},
		{Path: domain + "/promotion-task.yaml", YAML: taskYAML(domain, opts.TaskName)},
		{Path: domain + "/warehouses.yaml", YAML: strings.Join(warehouses, "---\n")},
		{Path: domain + "/stages.yaml", YAML: strings.Join(stages, "---\n")},
	}
	return files, skipped, c
}

func stageName(service, env string) string { return service + "-" + env }

// line is one artifact line: the environments that pull from a single image
// repository, in promotion order. first is the index the line starts at, so
// the first line can be told from the rest.
type line struct {
	first int
	envs  []*catalog.Deployment
}

// lines splits deployments, already in promotion order, wherever the image
// repository changes.
func lines(deployed []*catalog.Deployment) []line {
	var out []line
	for i, d := range deployed {
		if i == 0 || d.Image != deployed[i-1].Image {
			out = append(out, line{first: i})
		}
		out[len(out)-1].envs = append(out[len(out)-1].envs, d)
	}
	return out
}

func projectYAML(domain string) string {
	return "apiVersion: kargo.akuity.io/v1alpha1\nkind: Project\nmetadata:\n  name: " + domain + "\n"
}

// taskYAML is the promotion shared by every stage in the project. One task per
// project rather than the same five steps copied into every stage: a domain
// with 79 services would otherwise carry 79 identical copies, and changing how
// promotion works would mean changing all of them.
func taskYAML(domain, name string) string {
	return fmt.Sprintf(`apiVersion: kargo.akuity.io/v1alpha1
kind: PromotionTask
metadata:
  name: %s
  namespace: %s
spec:
  vars:
    - name: gitRepo
    - name: branch
    - name: imageRepo
    - name: appPath
    - name: appName
  steps:
    - uses: git-clone
      config:
        repoURL: ${{ vars.gitRepo }}
        checkout:
          - branch: ${{ vars.branch }}
            path: ./repo
    - uses: kustomize-set-image
      as: update-image
      config:
        path: ./repo/${{ vars.appPath }}
        images:
          - image: ${{ vars.imageRepo }}
            tag: ${{ imageFrom(vars.imageRepo).Tag }}
    - uses: git-commit
      as: commit
      config:
        path: ./repo
        message: ${{ task.outputs['update-image'].commitMessage }}
    - uses: git-push
      config:
        path: ./repo
    - uses: argocd-update
      config:
        apps:
          - name: ${{ vars.appName }}
            sources:
              - repoURL: ${{ vars.gitRepo }}
                desiredRevision: ${{ task.outputs.commit.commit }}
`, name, domain)
}

func warehouseYAML(service, domain, image string, opts Options) string {
	return fmt.Sprintf(`apiVersion: kargo.akuity.io/v1alpha1
kind: Warehouse
metadata:
  name: %s
  namespace: %s
spec:
  # Long on purpose. Tide asks this warehouse to look as soon as a pipeline
  # reports a new image, so the interval is a safety net for a missed
  # notification, not the way images are normally found.
  interval: 1h
  freightCreationPolicy: Automatic
  subscriptions:
    - image:
        repoURL: %s
        discoveryLimit: 20
        # Stated rather than left to Kargo, whose default is SemVer: against
        # tags that are not semantic versions that discovers nothing at all,
        # and a warehouse that finds nothing looks exactly like one whose
        # credentials are wrong.
        imageSelectionStrategy: %s
        # allowTagsRegexes, not allowTags: the singular form was removed in
        # Kargo v1.11 and a warehouse still using it fails discovery outright.
        allowTagsRegexes:
          - %q
`, service, domain, image, opts.ImageStrategy, opts.TagPattern)
}

// stageYAML writes one stage. from is the stage this one is promoted from;
// empty means it takes freight straight from the warehouse, which is what the
// environment a pipeline pushes to must do — a stage fed by another stage can
// never receive a freshly built image.
func stageYAML(service, warehouse, domain string, d *catalog.Deployment, from string, opts Options) string {
	sources := "        direct: true"
	if from != "" {
		sources = "        stages:\n          - " + from
	}
	branch := d.Revision
	if branch == "" {
		branch = "main"
	}
	return fmt.Sprintf(`apiVersion: kargo.akuity.io/v1alpha1
kind: Stage
metadata:
  name: %s
  namespace: %s
  labels:
    %s: %s
    %s: %s
spec:
  requestedFreight:
    - origin:
        kind: Warehouse
        name: %s
      sources:
%s
  promotionTemplate:
    spec:
      steps:
        - task:
            name: %s
          vars:
            - name: gitRepo
              value: %s
            - name: branch
              value: %s
            - name: imageRepo
              value: %s
            - name: appPath
              value: %s
            - name: appName
              value: %s
`, stageName(service, d.Env), domain,
		opts.ServiceLabel, service, opts.EnvLabel, d.Env,
		warehouse, sources, opts.TaskName,
		d.Repo, branch, d.Image, d.RepoPath, d.App)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
