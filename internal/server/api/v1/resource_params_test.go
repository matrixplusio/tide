package v1

import "testing"

// The manifest viewer identifies a resource by group/version/kind, and those
// go through a whitelist. A whitelist that is too narrow does not report an
// error a person can act on — the row simply refuses to open — so the kinds
// that matter are pinned here.
func TestTheResourceWhitelistAcceptsTheKindsWeActuallyHave(t *testing.T) {
	groups := map[string]bool{
		"":                          true, // core: ConfigMap, Service, Secret
		"apps":                      true,
		"gateway.networking.k8s.io": true, // HTTPRoute
		"networking.k8s.io":         true,
		"bitnami.com":               true, // SealedSecret
		"external-secrets.io":       true,
		"argoproj.io":               true,
		"autoscaling":               true,
		"UPPER":                     false,
		"has space":                 false,
		"-leading":                  false,
		"trailing-":                 false,
	}
	for in, want := range groups {
		if in == "" {
			continue // the empty group is allowed by the caller, not the regex
		}
		if got := reAPIGroup.MatchString(in); got != want {
			t.Errorf("group %q: accepted=%v, want %v", in, got, want)
		}
	}

	versions := map[string]bool{
		"v1": true, "v2": true, "v1beta1": true, "v1alpha2": true, "v2beta3": true,
		"beta1": false, "1": false, "v1beta": false, "v1-beta1": false,
	}
	for in, want := range versions {
		if got := reAPIVersion.MatchString(in); got != want {
			t.Errorf("version %q: accepted=%v, want %v", in, got, want)
		}
	}

	kinds := map[string]bool{
		"ConfigMap": true, "Secret": true, "HTTPRoute": true, "SealedSecret": true,
		"Deployment": true, "StatefulSet": true, "HorizontalPodAutoscaler": true,
		"ExternalSecret": true, "Service": true,
		"configMap": false, "Config Map": false, "": false, "Foo-Bar": false,
	}
	for in, want := range kinds {
		if got := reAPIKind.MatchString(in); got != want {
			t.Errorf("kind %q: accepted=%v, want %v", in, got, want)
		}
	}
}
