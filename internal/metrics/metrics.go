// Package metrics exposes Prometheus metrics for Tide itself: HTTP traffic,
// releases, the executor and calls to upstreams. Scraping is opt-in through
// server.metrics_addr / server.metrics_token (see configs/tide.yaml).
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds Tide's collectors plus the Go runtime ones.
var Registry = prometheus.NewRegistry()

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tide_http_requests_total",
		Help: "HTTP requests by route template, method and status class.",
	}, []string{"route", "method", "status"})

	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tide_http_request_duration_seconds",
		Help:    "HTTP request duration by route template.",
		Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"route", "method"})

	upstreamRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tide_upstream_requests_total",
		Help: "Calls to Kargo / Argo CD / registries by host, method and outcome (status code, or an error kind).",
	}, []string{"host", "method", "outcome"})

	upstreamDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tide_upstream_request_duration_seconds",
		Help:    "Upstream call duration by host.",
		Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"host"})

	releasesFinished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tide_releases_finished_total",
		Help: "Releases that reached a terminal state, by environment and status.",
	}, []string{"env", "status"})

	releaseDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tide_release_execution_seconds",
		Help:    "Time from confirmation (or approval) to the final state.",
		Buckets: []float64{10, 30, 60, 120, 300, 600, 1200, 1800, 3600},
	}, []string{"env", "status"})

	itemsExecuting = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "tide_items_executing",
		Help: "Release items the executor is currently running.",
	})

	releasesWaiting = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tide_releases_waiting",
		Help: "Releases waiting for something: confirmation or approval.",
	}, []string{"state"})

	// buildInfo is the usual "constant metric": the value is always 1 and the
	// answer is in the labels, so a dashboard can say which build produced
	// the rest of the numbers.
	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tide_build_info",
		Help: "Always 1; the build is in the labels.",
	}, []string{"version", "commit", "go"})

	catalogRefresh = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tide_catalog_refresh_seconds",
		Help:    "Service catalog refresh duration, by outcome.",
		Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"outcome"})
)

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		httpRequests, httpDuration, upstreamRequests, upstreamDuration,
		releasesFinished, releaseDuration, itemsExecuting, releasesWaiting, catalogRefresh,
		buildInfo,
	)
}

// BuildInfo records what this binary is. Called once at startup.
func BuildInfo(ver, commit, goVersion string) {
	buildInfo.WithLabelValues(ver, commit, goVersion).Set(1)
}

// Handler serves the metrics. With a token, scrapers must send it as
// "Authorization: Bearer <token>".
func Handler(token string) http.Handler {
	h := promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
	if token == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// HTTP records one served request. Route is the template ("" for no route),
// so the label stays bounded.
func HTTP(route, method string, status int, d time.Duration) {
	if route == "" {
		route = "other"
	}
	httpRequests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	httpDuration.WithLabelValues(route, method).Observe(d.Seconds())
}

// Upstream records one call to an external system. outcome is the status code
// or an error kind ("timeout", "error").
func Upstream(host, method, outcome string, d time.Duration) {
	upstreamRequests.WithLabelValues(host, method, outcome).Inc()
	upstreamDuration.WithLabelValues(host).Observe(d.Seconds())
}

// ReleaseFinished records a terminal release. since is the confirmation time;
// zero skips the duration.
func ReleaseFinished(env, status string, since, until time.Time) {
	releasesFinished.WithLabelValues(env, status).Inc()
	if !since.IsZero() && until.After(since) {
		releaseDuration.WithLabelValues(env, status).Observe(until.Sub(since).Seconds())
	}
}

// ExecutorState publishes what the executor is working on and what waits.
func ExecutorState(executingItems, confirming, approving int) {
	itemsExecuting.Set(float64(executingItems))
	releasesWaiting.WithLabelValues("confirming").Set(float64(confirming))
	releasesWaiting.WithLabelValues("approving").Set(float64(approving))
}

// CatalogRefresh records a catalog rebuild.
func CatalogRefresh(d time.Duration, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	catalogRefresh.WithLabelValues(outcome).Observe(d.Seconds())
}
