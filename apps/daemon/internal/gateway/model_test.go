package gateway

import (
	"bufio"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

const upstreamKey = "sk-upstream-key"

// startModel serves one Anthropic upstream with key at the fake TLS
// upstream's /anthropic.
func startModel(t *testing.T, key string, handler http.HandlerFunc) (string, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	eps := serveOnLoopback(t, Config{
		Model:   modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: srv.URL + "/anthropic", APIKey: key},
		RootCAs: trust(srv),
	})
	return eps.Model, srv
}

func TestModelInjectsTheKeyAndNeverThePlaceholder(t *testing.T) {
	seen := make(chan *http.Request, 1)
	base, _ := startModel(t, upstreamKey, func(w http.ResponseWriter, r *http.Request) {
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

func TestModelWithholdsTheKeyFromResponseHeaders(t *testing.T) {
	// The upstream echoes the key it received in an informational response,
	// in headers and in a trailer. A configured key with surrounding
	// whitespace reaches the upstream trimmed.
	for _, key := range []string{upstreamKey, upstreamKey + " \t"} {
		withholdsTheKey(t, key)
	}
}

func withholdsTheKey(t *testing.T, key string) {
	base, _ := startModel(t, key, func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Api-Key")
		w.Header().Set("Link", "<https://cdn.invalid/"+key+">; rel=preload")
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Del("Link")
		w.Header().Set("Trailer", "X-Echo, X-Done")
		w.Header().Set("X-Echo", "key="+key)
		w.Header().Set("Location", "https://elsewhere.invalid/?key="+key)
		w.Header().Set("X-Native", "kept")
		io.WriteString(w, "answer")
		w.Header().Set("X-Echo", key)
		w.Header().Set("X-Done", "yes")
	})
	var informational []http.Header
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		Got1xxResponse: func(_ int, h textproto.MIMEHeader) error {
			informational = append(informational, http.Header(h).Clone())
			return nil
		},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", base+"/v1/messages", strings.NewReader("{}"))
	resp, err := noRedirects.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "answer" || resp.Header.Get("X-Native") != "kept" || resp.Trailer.Get("X-Done") != "yes" || len(informational) != 1 {
		t.Fatalf("key %q: answer %d %q, header %v, trailer %v, %d informational", key, resp.StatusCode, body, resp.Header, resp.Trailer, len(informational))
	}
	for _, h := range append(informational, resp.Header, resp.Trailer) {
		for name, values := range h {
			for _, v := range values {
				if strings.Contains(v, upstreamKey) {
					t.Errorf("key %q: the Harness received the key in %s", key, name)
				}
			}
		}
	}
}

func TestModelKeepsTheKeyOutOfTheLog(t *testing.T) {
	// The upstream follows an empty answer with bytes that echo the key,
	// which http.Transport logs if the connection then sits idle.
	closed := make(chan struct{})
	base, _ := startModel(t, upstreamKey, func(w http.ResponseWriter, r *http.Request) {
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		rw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\nHTTP/1.1 200 OK\r\nX-Echo: " + r.Header.Get("X-Api-Key") + "\r\n\r\n")
		rw.Flush()
		io.Copy(io.Discard, c)
		close(closed)
	})
	logged := &syncBuffer{}
	previous := log.Writer()
	log.SetOutput(logged)
	t.Cleanup(func() { log.SetOutput(previous) })

	resp, err := noRedirects.Post(base+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("answer %d", resp.StatusCode)
	}
	select {
	case <-closed:
	case <-time.After(wait):
		t.Fatal("the gateway kept the upstream connection")
	}
	if strings.Contains(logged.String(), upstreamKey) {
		t.Fatal("the key reached the log")
	}
}

// syncBuffer collects what the global logger writes.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestModelRejectsUndeclaredRequests(t *testing.T) {
	var hits atomic.Int32
	base, _ := startModel(t, upstreamKey, func(http.ResponseWriter, *http.Request) { hits.Add(1) })
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
	base, _ := startModel(t, upstreamKey, func(w http.ResponseWriter, r *http.Request) {
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
	base, _ := startModel(t, upstreamKey, func(w http.ResponseWriter, r *http.Request) {
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
