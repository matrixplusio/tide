// Command apicheck drives the local API with a cookie jar: a stand-in for the
// browser when the local ingress is down. Dev only; not part of the binary.
//
//	go run ./hack/apicheck -u dev1 -p 'secret' GET /me
//	go run ./hack/apicheck -u admin -p 'secret' POST /role-bindings '{"roleId":"operator",...}'
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
)

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "server base URL")
	user := flag.String("u", "", "username")
	pass := flag.String("p", "", "password")
	flag.Parse()
	args := flag.Args()
	if len(args) < 2 || *user == "" {
		fmt.Fprintln(os.Stderr, "usage: apicheck -u USER -p PASS METHOD PATH [BODY]")
		os.Exit(2)
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	do := func(method, path, body string) (int, string) {
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(context.Background(), method, *base+path, r)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", *base)
		resp, err := c.Do(req)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(bytes.TrimSpace(b))
	}
	if code, out := do("POST", "/api/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, *user, *pass)); code != 200 {
		fmt.Fprintln(os.Stderr, "login failed:", out)
		os.Exit(1)
	}
	body := ""
	if len(args) > 2 {
		body = args[2]
	}
	_, out := do(strings.ToUpper(args[0]), "/api/v1"+args[1], body)
	fmt.Println(out)
}
