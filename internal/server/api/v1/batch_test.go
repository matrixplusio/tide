package v1

import (
	"encoding/json"
	"errors"
	"testing"

	"tide/internal/catalog"
	"tide/internal/release"
	"tide/internal/server/api/errcode"
	"tide/internal/settings"
	"tide/internal/validate"
)

func TestBatchViolation(t *testing.T) {
	cat := settings.Catalog{BatchDimension: "role", Dimensions: []settings.Dimension{{Key: "role", Name: "类型",
		Values: []settings.DimensionValue{{Value: "backend", Name: "后端"}, {Value: "frontend", Name: "前端"}}}}}
	svcs := map[string]*catalog.Service{
		"api":     {Name: "api", Project: "acme", Dimensions: map[string]string{"role": "backend"}},
		"worker":  {Name: "worker", Project: "acme", Dimensions: map[string]string{"role": "backend"}},
		"web":     {Name: "web", Project: "acme", Dimensions: map[string]string{"role": "frontend"}},
		"admin":   {Name: "admin", Project: "acme", Dimensions: map[string]string{"role": "frontend"}},
		"gateway": {Name: "gateway", Project: "internal", Dimensions: map[string]string{"role": "backend"}},
		"loose":   {Name: "loose", Dimensions: map[string]string{"role": "backend"}},
		"tool":    {Name: "tool", Project: "acme", Dimensions: map[string]string{}},
	}
	find := func(n string) *catalog.Service { return svcs[n] }
	items := func(kind string, names ...string) []releaseItemReq {
		out := make([]releaseItemReq, len(names))
		for i, n := range names {
			out[i] = releaseItemReq{Kind: kind, Service: n}
		}
		return out
	}
	inFlight := func(service string) []release.Release {
		p, _ := json.Marshal(release.RestartPayload{Service: service, Env: "qa"})
		return []release.Release{{ID: "REL-20260917-009", Env: "qa", Items: []release.Item{{Kind: release.KindRestart, Payload: p}}}}
	}
	fields := func(err error) string {
		var fe validate.Errors
		if errors.As(err, &fe) {
			return fe[0].Field
		}
		return ""
	}

	tests := []struct {
		name   string
		noDim  bool // run without a batch dimension
		items  []releaseItemReq
		active []release.Release
		field  string       // expected field error, or
		code   errcode.Code // expected API code, or neither for success
	}{
		{name: "same project and type", items: items("image", "api", "worker")},
		{name: "mixed kinds", items: []releaseItemReq{{Kind: "image", Service: "api"}, {Kind: "restart", Service: "worker"}}, field: "items.1.kind"},
		{name: "two projects", items: items("image", "api", "gateway"), field: "items.1.service"},
		{name: "no project", items: items("image", "loose", "api"), field: "items.1.service"},
		{name: "two types", items: items("image", "api", "web"), field: "items.1.service"},
		{name: "untyped in batch", items: items("image", "tool", "api"), field: "items.1.service"},
		{name: "frontend waits for backend", items: items("restart", "web", "admin"), active: inFlight("api"), code: errcode.OrderBlocked},
		{name: "single frontend also waits", items: items("image", "web"), active: inFlight("worker"), code: errcode.OrderBlocked},
		{name: "backend in another project does not block", items: items("image", "web"), active: inFlight("gateway")},
		{name: "backend may start while frontend runs", items: items("image", "api"), active: inFlight("web")},
		{name: "no batch dimension: types may mix", noDim: true, items: items("image", "api", "web"), active: inFlight("api")},
		{name: "no batch dimension: still one project", noDim: true, items: items("image", "api", "gateway"), field: "items.1.service"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := cat
			if tt.noDim {
				c = settings.Catalog{}
			}
			err := batchViolation(c, find, "qa", tt.items, tt.active)
			switch {
			case tt.field != "":
				if got := fields(err); got != tt.field {
					t.Fatalf("want field %s, got %v", tt.field, err)
				}
			case tt.code != 0:
				if got := errcode.From(err).Code; got != tt.code {
					t.Fatalf("want code %d, got %v", tt.code, err)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
			}
		})
	}
}
