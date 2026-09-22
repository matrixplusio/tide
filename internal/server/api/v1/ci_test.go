package v1

import (
	"errors"
	"strings"
	"testing"

	"tide/internal/auth"
	"tide/internal/release"
	"tide/internal/store/pg"
	"tide/internal/validate"
)

func validCIReq() ciReleaseReq {
	return ciReleaseReq{
		Service: "order-api", Env: "qa",
		Digest:   "sha256:" + strings.Repeat("a", 64),
		Image:    "registry.example.com/acme/order-api:1.2.3",
		Commit:   "0a1b2c3",
		Pipeline: "https://gitlab.example.com/acme/web-portal/-/pipelines/1",
		Actor:    "someone", JiraTicket: "OPS-1", Reason: "roll out the fix",
	}
}

// A pipeline usually has "repo@sha256:..." to hand rather than the bare
// digest, so the handler accepts either and stores one shape.
func TestCIRequestNormalizesTheDigest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, given := range []string{digest, "registry.example.com/app@" + digest, strings.ToUpper(digest), "  " + digest + "  "} {
		r := validCIReq()
		r.Digest = given
		r.Normalize()
		if r.Digest != digest {
			t.Fatalf("%q normalized to %q", given, r.Digest)
		}
		if err := r.Check(); err != nil {
			t.Fatalf("%q: %v", given, err)
		}
	}
}

func TestCIRequestNormalizesTheTicket(t *testing.T) {
	r := validCIReq()
	r.JiraTicket = "  ops-42  "
	r.Normalize()
	if r.JiraTicket != "OPS-42" {
		t.Fatalf("got %q", r.JiraTicket)
	}
}

// Everything a pipeline sends is untrusted input like any other body.
func TestCIRequestRejectsMalformedFields(t *testing.T) {
	tests := []struct {
		name   string
		break_ func(*ciReleaseReq)
		field  string
	}{
		{"service", func(r *ciReleaseReq) { r.Service = "Bad Name" }, "service"},
		{"env", func(r *ciReleaseReq) { r.Env = "QA!" }, "env"},
		{"digest", func(r *ciReleaseReq) { r.Digest = "sha256:short" }, "digest"},
		{"unprefixed digest", func(r *ciReleaseReq) { r.Digest = strings.Repeat("a", 64) }, "digest"},
		{"image", func(r *ciReleaseReq) { r.Image = "registry.example.com/APP:1 2" }, "image"},
		{"commit", func(r *ciReleaseReq) { r.Commit = "nothex" }, "commit"},
		{"pipeline scheme", func(r *ciReleaseReq) { r.Pipeline = "javascript:alert(1)" }, "pipeline"},
		{"pipeline credentials", func(r *ciReleaseReq) { r.Pipeline = "https://u:p@gitlab.example.com/x" }, "pipeline"},
		{"actor", func(r *ciReleaseReq) { r.Actor = "some\x00one" }, "actor"},
		{"ticket", func(r *ciReleaseReq) { r.JiraTicket = "NOTATICKET" }, "jiraTicket"},
	}
	for _, tt := range tests {
		r := validCIReq()
		tt.break_(&r)
		r.Normalize()
		err := r.Check()
		if err == nil {
			t.Fatalf("%s: accepted", tt.name)
		}
		var errs validate.Errors
		if !errors.As(err, &errs) {
			t.Fatalf("%s: got %T, want field errors", tt.name, err)
		}
		if !hasField(errs, tt.field) {
			t.Fatalf("%s: errors %v do not name %q", tt.name, errs, tt.field)
		}
	}
}

// All the field errors come back at once (CONVENTIONS.md §4.4), not one per call.
func TestCIRequestReportsEveryBadFieldAtOnce(t *testing.T) {
	r := validCIReq()
	r.Service, r.Env, r.Digest = "Bad", "QA!", "nope"
	r.Normalize()
	var errs validate.Errors
	if !errors.As(r.Check(), &errs) || len(errs) < 3 {
		t.Fatalf("got %v, want an error for each of service, env and digest", errs)
	}
}

// The generated job must not fail a build and must not carry the token.
func TestCISnippetIsSafeToPasteIntoAPipeline(t *testing.T) {
	s := ciSnippet("https://tide.example.com/api/v1/ci/releases", "qa")
	for _, want := range []string{`\"env\":\"qa\"`, "Idempotency-Key", "$TIDE_TOKEN", "|| echo", "TIDE_WEBHOOK_URL"} {
		if !strings.Contains(s, want) {
			t.Fatalf("snippet is missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "tide_ci_") {
		t.Fatalf("snippet carries a token:\n%s", s)
	}
}

func hasField(errs validate.Errors, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}

// The confirm and cancel handlers and the "can" flags the page draws its
// buttons from must agree; a button the server refuses is worse than none.
func TestOwnershipOfAReleaseDependsOnItsSource(t *testing.T) {
	creator := &auth.User{User: pg.User{Sub: "local:alice"}}
	other := &auth.User{User: pg.User{Sub: "local:bob"}}
	ui := &release.Release{CreatedBy: "local:alice", Source: release.SourceUI}
	fromCI := &release.Release{CreatedBy: "ci:abc", Source: release.SourceCI}

	if !ownedBy(ui, creator) {
		t.Fatal("the creator does not own their own release")
	}
	if ownedBy(ui, other) {
		t.Fatal("somebody else owns a release a person created")
	}
	if !ownedBy(fromCI, other) || !ownedBy(fromCI, creator) {
		t.Fatal("a CI release is owned by nobody, so anyone with the permission acts on it")
	}
}
