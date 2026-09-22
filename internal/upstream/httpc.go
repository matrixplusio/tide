// Package upstream is the only place Tide talks to external systems (Kargo,
// Argo CD, registries). Tide never talks to a Kubernetes API directly.
package upstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tide/internal/metrics"
)

// HTTPError keeps the upstream response body verbatim: operators want the
// original error text to search for, not a wrapped friendly message.
type HTTPError struct {
	Method string
	URL    string
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, strings.TrimSpace(e.Body))
}

func NewHTTPClient(insecure bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		// Opt-in per upstream, for internal endpoints with self-signed certs.
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit admin setting
	}
	return &http.Client{Transport: measured{tr}, Timeout: 30 * time.Second}
}

// measured counts every upstream call: how many, how slow, how they ended.
// Labels stay bounded (host and method, not the path).
type measured struct{ next http.RoundTripper }

func (m measured) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := m.next.RoundTrip(req)
	outcome := "error"
	switch {
	case err != nil && errors.Is(err, context.DeadlineExceeded):
		outcome = "timeout"
	case err == nil:
		outcome = strconv.Itoa(resp.StatusCode)
	}
	metrics.Upstream(req.URL.Host, req.Method, outcome, time.Since(start))
	return resp, err
}

// DoJSON sends in (if non-nil) as JSON and decodes the response into out.
func DoJSON(ctx context.Context, c *http.Client, method, url string, header http.Header, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return &HTTPError{Method: method, URL: url, Status: resp.StatusCode, Body: string(b)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
