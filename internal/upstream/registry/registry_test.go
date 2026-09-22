package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// An expired credential and an unreachable registry both end with blank
// versions on the page. Telling them apart is the whole difference between
// "go and reissue the token" and "go and look at the network", so the
// distinction has to survive as far as the caller.
func TestInspectMarksRefusalsAsAuthentication(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// No Bearer challenge: the registry is simply saying no, which is
			// what a registry does once a password stops working.
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"errors":[{"code":"UNAUTHORIZED"}]}`))
		}))
		_, err := New(srv.URL, "u", "p", false).Inspect(context.Background(), "acme/order-api", "v1")
		srv.Close()
		if err == nil {
			t.Fatalf("HTTP %d reported success", status)
		}
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("HTTP %d did not come back as an authentication failure: %v", status, err)
		}
	}
}

// And the opposite: a registry that answers with a server error is not an
// authentication problem, and must not send anyone off to reissue a working
// credential.
func TestInspectDoesNotCallServerErrorsAuthentication(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "u", "p", false).Inspect(context.Background(), "acme/order-api", "v1")
	if err == nil {
		t.Fatal("HTTP 502 reported success")
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Fatalf("HTTP 502 was reported as an authentication failure: %v", err)
	}
}
