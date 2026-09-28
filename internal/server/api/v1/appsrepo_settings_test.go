package v1

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tide/internal/settings"
	"tide/internal/validate"
)

// The manifest repository's form saved with a 500 on every attempt: the type
// switch that validates a section had no case for it, and "unknown settings
// type" is what a person got for filling the form in correctly. Every section
// the table offers must be one the validator knows.
func TestEverySettingsSectionValidates(t *testing.T) {
	a := &API{}
	for name, def := range sections {
		// Several validators read the store to check a section against
		// another; with no store that is a nil dereference, which is fine
		// here — it proves the switch recognised the type. Only reaching the
		// default branch is the failure this test is about.
		err := func() (err error) {
			defer func() {
				if recover() != nil {
					err = nil
				}
			}()
			return a.validateSection(context.Background(), def.make())
		}()
		if err != nil && strings.Contains(err.Error(), "unknown settings type") {
			t.Errorf("section %q: the validator does not know its type", name)
		}
	}
}

func TestAppsRepoValidation(t *testing.T) {
	a := &API{}
	fields := func(err error) []string {
		var ve validate.Errors
		if !errors.As(err, &ve) {
			t.Fatalf("expected field errors, got %v", err)
		}
		var out []string
		for _, f := range ve {
			out = append(out, f.Field)
		}
		return out
	}

	// Empty: everything required is named, and nothing else.
	got := fields(a.validateSection(context.Background(), &settings.AppsRepo{}))
	for _, want := range []string{"baseUrl", "project", "branch", "token"} {
		found := false
		for _, f := range got {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Errorf("empty form: %s not reported (got %v)", want, got)
		}
	}

	// Complete: accepted, and the provider defaulted.
	cfg := &settings.AppsRepo{BaseURL: "https://git.example.com", Project: "acme/k8s-apps", Branch: "main", Token: "t"}
	if err := a.validateSection(context.Background(), cfg); err != nil {
		t.Fatalf("a complete form must pass: %v", err)
	}
	if cfg.Provider != settings.ProviderGitLab {
		t.Errorf("provider not defaulted: %q", cfg.Provider)
	}

	// A registry path that forgets the placeholder would point every line at
	// one file.
	cfg.Registry = "services.yaml"
	if got := fields(a.validateSection(context.Background(), cfg)); len(got) != 1 || got[0] != "registry" {
		t.Errorf("registry without {line}: got %v", got)
	}
}
