package gateway

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestProxyConnectsOnlyThroughTheSandbox(t *testing.T) {
	sb := startSandbox(t)
	eps := serveOnLoopback(t, Config{Model: model, OpenNetwork: sb.open, Proxy: true})
	hello := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello "+r.RequestURI) })
	secure := httptest.NewTLSServer(hello)
	defer secure.Close()
	plain := httptest.NewServer(hello)
	defer plain.Close()

	proxyURL, _ := url.Parse(eps.Proxy)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: trust(secure)}}, Timeout: wait}
	// A forwarded query reaches the destination as sent, parameters with
	// semicolons included.
	for _, target := range []string{secure.URL + "/via-connect", plain.URL + "/via-forward?filter=a;b&keep=1"} {
		resp, err := client.Get(target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if u, _ := url.Parse(target); resp.StatusCode != 200 || string(body) != "hello "+u.RequestURI() {
			t.Errorf("%s: %d %q", target, resp.StatusCode, body)
		}
	}
	if n := sb.dials.Load(); n != 2 {
		t.Errorf("the sandbox made %d connections, want 2", n)
	}

	// An origin-form request is not a proxy request.
	resp, err := noRedirects.Get(eps.Proxy + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("origin-form request: %d", resp.StatusCode)
	}
}
