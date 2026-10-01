package gateway

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

const upstreamKey = "sk-upstream-key"

// startModel serves one Anthropic upstream at the fake TLS upstream's /anthropic.
func startModel(t *testing.T, handler http.HandlerFunc) (string, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	eps := serveOnLoopback(t, Config{
		Models: []Model{{Name: "main", Provider: modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: srv.URL + "/anthropic", APIKey: upstreamKey}}},
		TLS:    trust(srv),
	})
	return eps.Models["main"], srv
}

func TestModelInjectsTheKeyAndNeverThePlaceholder(t *testing.T) {
	seen := make(chan *http.Request, 1)
	base, _ := startModel(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		seen <- r
		io.WriteString(w, "answer")
	})
	req, _ := http.NewRequest("POST", base+"/v1/messages?beta=true", strings.NewReader(`{"model":"m"}`))
	req.Header["authorization"] = []string{"Bearer " + modelprovider.Placeholder}
	req.Header["X-API-KEY"] = []string{modelprovider.Placeholder, modelprovider.Placeholder}
	req.Header.Set("Cookie", "session="+modelprovider.Placeholder)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	resp, err := noRedirects.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if body, _ := io.ReadAll(resp.Body); resp.StatusCode != 200 || string(body) != "answer" {
		t.Fatalf("answer %d %q", resp.StatusCode, body)
	}
	r := <-seen
	if r.URL.RequestURI() != "/anthropic/v1/messages?beta=true" {
		t.Errorf("upstream target %q", r.URL.RequestURI())
	}
	if got := r.Header.Values("X-Api-Key"); len(got) != 1 || got[0] != upstreamKey {
		t.Errorf("upstream X-Api-Key %q", got)
	}
	if r.Header.Get("Anthropic-Version") != "2023-06-01" {
		t.Error("a native header was not relayed")
	}
	for name, values := range r.Header {
		for _, v := range values {
			if strings.Contains(v, modelprovider.Placeholder) {
				t.Errorf("upstream received the placeholder in %s", name)
			}
		}
	}
	if body, _ := io.ReadAll(r.Body); string(body) != `{"model":"m"}` {
		t.Errorf("upstream body %q", body)
	}
}

func TestModelRejectsUndeclaredRequests(t *testing.T) {
	var hits atomic.Int32
	base, _ := startModel(t, func(http.ResponseWriter, *http.Request) { hits.Add(1) })
	cases := []struct {
		method, path string
		upgrade      bool
		want         int
	}{
		{"GET", "/v1/messages", false, http.StatusMethodNotAllowed},
		{"POST", "/v1/complete", false, http.StatusNotFound},
		{"POST", "/anthropic/v1/messages", false, http.StatusNotFound},
		{"POST", "/v1/%6Dessages", false, http.StatusNotFound},
		{"POST", "/v1/messages", true, http.StatusBadRequest},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(c.method, base+c.path, nil)
		if c.upgrade {
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
		}
		resp, err := noRedirects.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s %s upgrade=%v: %d, want %d", c.method, c.path, c.upgrade, resp.StatusCode, c.want)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the upstream was reached %d times", n)
	}
}

func TestModelStreamsEvents(t *testing.T) {
	// The upstream holds the second event until the first reached the
	// Harness, or until the wait ends.
	release := make(chan struct{})
	var once sync.Once
	var released atomic.Bool
	timer := time.AfterFunc(wait, func() {
		released.Store(true)
		once.Do(func() { close(release) })
	})
	base, _ := startModel(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "data: two\n\n")
	})
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Post(base+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: one\n" {
		t.Fatalf("first event %q, %v", line, err)
	}
	if released.Load() {
		t.Fatal("the first event arrived only after the upstream finished")
	}
	timer.Stop()
	once.Do(func() { close(release) })
}

func TestModelReturnsRedirectsUnfollowed(t *testing.T) {
	var hits atomic.Int32
	const location = "https://elsewhere.invalid/v1/messages?x=1"
	base, _ := startModel(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, location, http.StatusTemporaryRedirect)
	})
	resp, err := noRedirects.Post(base+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != location {
		t.Errorf("answer %d Location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("the upstream was reached %d times", n)
	}
}
