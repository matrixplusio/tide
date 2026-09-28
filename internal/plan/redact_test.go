package plan

import (
	"encoding/json"
	"strings"
	"testing"
)

func obj(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A live Secret holds base64, which is not encryption: showing it to whoever
// can open a service page hands them the password.
func TestASecretShowsItsKeysAndNotItsValues(t *testing.T) {
	m := obj(t, `{"kind":"Secret","metadata":{"name":"db"},
		"data":{"password":"c3VwZXItc2VjcmV0"},"stringData":{"token":"plain"}}`)
	RedactSecret(m)
	out, err := ManifestYAML(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"c3VwZXItc2VjcmV0", "plain"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("a secret value reached the manifest:\n%s", out)
		}
	}
	// The keys are the point: "is the password even in there?"
	for _, want := range []string{"password", "token"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the key %q is missing, so the manifest cannot answer whether it is set:\n%s", want, out)
		}
	}
}

// A SealedSecret carries ciphertext, and it is the only place a reader can
// check that the sealed value was actually updated.
func TestASealedSecretIsNotRedacted(t *testing.T) {
	m := obj(t, `{"kind":"SealedSecret","metadata":{"name":"db"},
		"spec":{"encryptedData":{"password":"AgBv1x..."}}}`)
	RedactSecret(m)
	out, err := ManifestYAML(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "AgBv1x...") {
		t.Fatalf("the ciphertext was hidden, leaving nothing to verify:\n%s", out)
	}
}

// The sync diff renders live and target the same way; redacting inside that
// path would make a changed secret look unchanged.
func TestTheSyncDiffStillSeesASecretValueChange(t *testing.T) {
	before, err := manifestYAML(`{"kind":"Secret","metadata":{"name":"db"},"data":{"password":"b25l"}}`)
	if err != nil {
		t.Fatal(err)
	}
	after, err := manifestYAML(`{"kind":"Secret","metadata":{"name":"db"},"data":{"password":"dHdv"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("a changed secret value renders identically, so the sync plan would say nothing changed")
	}
}

// 脱敏只认 kind=Secret，别的 kind 一律不碰。
func TestRedactTouchesOnlySecrets(t *testing.T) {
	cases := []struct {
		name, raw, needle string
	}{
		{"ConfigMap 的 data 不动", `{"kind":"ConfigMap","data":{"url":"jdbc:mysql://db:3306/app"}}`, "jdbc:mysql://db:3306/app"},
		{"SealedSecret 不动", `{"kind":"SealedSecret","spec":{"encryptedData":{"p":"AgBv1x"}}}`, "AgBv1x"},
		{"ExternalSecret 不动", `{"kind":"ExternalSecret","spec":{"data":[{"remoteRef":{"key":"demo/dev/cache"}}]}}`, "demo/dev/cache"},
		{"Deployment 的 env 不动", `{"kind":"Deployment","spec":{"template":{"spec":{"containers":[{"env":[{"name":"P","value":"hunter2"}]}]}}}}`, "hunter2"},
		{"HTTPRoute 不动", `{"kind":"HTTPRoute","spec":{"hostnames":["svc.example.test"]}}`, "svc.example.test"},
		{"Secret 的非值字段不动", `{"kind":"Secret","type":"kubernetes.io/dockerconfigjson","metadata":{"name":"reg"},"data":{"x":"eQ=="}}`, "kubernetes.io/dockerconfigjson"},
	}
	for _, c := range cases {
		m := obj(t, c.raw)
		RedactSecret(m)
		out, err := ManifestYAML(m)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !strings.Contains(out, c.needle) {
			t.Errorf("%s: %q 被误伤了\n%s", c.name, c.needle, out)
		}
	}
}
