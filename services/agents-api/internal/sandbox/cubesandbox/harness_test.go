package cubesandbox

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

const (
	testKey   = "synthetic-cube-api-key"
	testToken = "synthetic-envd-access-token"
	testCred  = "synthetic-runner-credential"
)

// message records one request the fake cluster received.
type message struct {
	Method string
	Path   string
	Host   string
	Auth   string
	Query  string
	Body   []byte
}

// cluster is an offline CubeAPI and CubeProxy stand-in. It records every request
// so the tests can prove what the adapter did not send, which matters more than
// what it did.
type cluster struct {
	t       *testing.T
	mu      sync.Mutex
	control *httptest.Server
	data    *httptest.Server

	domain       string
	sandboxes    []map[string]any
	controlCalls []message
	dataCalls    []message

	// Scripted behaviour. The zero value is the happy path.
	redirect       bool
	createBody     string
	createStatus   int
	deleteStatus   int
	deleteKeeps    bool
	listOverride   []map[string]any
	detailOverride map[string]any
	readiness      int
	refreshStatus  int
	stream         func(w http.ResponseWriter)
}

func newCluster(t *testing.T) *cluster {
	t.Helper()
	c := &cluster{t: t, domain: "cube.app", readiness: http.StatusOK}
	c.control = httptest.NewServer(http.HandlerFunc(c.serveControl))
	c.data = httptest.NewServer(http.HandlerFunc(c.serveData))
	t.Cleanup(func() {
		c.control.Close()
		c.data.Close()
	})
	return c
}

// provider builds a provider pointed at the fake cluster.
func (c *cluster) provider(installation string, mutate ...func(*Config)) *Provider {
	c.t.Helper()
	config := Config{
		InstallationID: installation,
		APIURL:         c.control.URL + "/cubeapi/v1",
		ProxyNodeIP:    strings.TrimPrefix(c.data.URL, "http://"),
		SandboxDomain:  c.domain,
		ProxyScheme:    "http",
		Template:       "tpl-pinned",
		APIKey:         testKey,
		LeaseSeconds:   43200,
		HostMountRoot:  "/data/shared/parsar",
	}
	for _, change := range mutate {
		change(&config)
	}
	provider, err := New(config)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(provider.Close)
	return provider
}

// bootstrap builds a valid Bootstrap for a fresh allocation.
func (c *cluster) bootstrap() sandbox.Bootstrap {
	return sandbox.Bootstrap{
		Reference:     sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()},
		SessionID:     uuid.NewString(),
		DeviceID:      uuid.NewString(),
		CoreURL:       "http://core.invalid/api/v1",
		Credential:    testCred,
		NetworkAccess: "enabled",
	}
}

func (c *cluster) metadata(installation string, r sandbox.Reference) map[string]string {
	return map[string]string{
		labelPrefix + "installation": installation,
		labelPrefix + "tenant":       r.TenantID,
		labelPrefix + "environment":  r.EnvironmentID,
		labelPrefix + "allocation":   r.AllocationID,
	}
}

// addSandbox seeds one sandbox exactly as CubeAPI would report it.
func (c *cluster) addSandbox(metadata map[string]string, state string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := "sbx-" + uuid.NewString()
	c.sandboxes = append(c.sandboxes, map[string]any{
		"sandboxID": id, "templateID": "tpl-pinned", "state": state, "domain": c.domain,
		"envdAccessToken": testToken, "metadata": metadata,
	})
	return id
}

func (c *cluster) remove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.sandboxes[:0]
	for _, entry := range c.sandboxes {
		if entry["sandboxID"] != id {
			kept = append(kept, entry)
		}
	}
	c.sandboxes = kept
}

func (c *cluster) setState(id, state string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.sandboxes {
		if entry["sandboxID"] == id {
			entry["state"] = state
		}
	}
}

// reset drops every seeded sandbox, keeping the scripted behaviour.
func (c *cluster) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sandboxes = nil
}

func (c *cluster) count(messages []message, marker string) int {
	count := 0
	for _, entry := range messages {
		if strings.Contains(entry.Path, marker) {
			count++
		}
	}
	return count
}

// creates counts sandbox creations only: a filtered list also contains
// "/sandboxes" and must never be mistaken for a replay.
func creates(messages []message) int {
	count := 0
	for _, entry := range messages {
		if entry.Method == http.MethodPost && strings.HasSuffix(entry.Path, "/sandboxes") {
			count++
		}
	}
	return count
}

func deletes(messages []message) int {
	count := 0
	for _, entry := range messages {
		if entry.Method == http.MethodDelete && strings.Contains(entry.Path, "/sandboxes/") {
			count++
		}
	}
	return count
}

func refreshes(messages []message) int {
	count := 0
	for _, entry := range messages {
		if entry.Method == http.MethodPost && strings.HasSuffix(entry.Path, "/refreshes") {
			count++
		}
	}
	return count
}

func (c *cluster) controlRequests() []message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]message(nil), c.controlCalls...)
}

func (c *cluster) dataRequests() []message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]message(nil), c.dataCalls...)
}

func (c *cluster) record(sink *[]message, r *http.Request) []byte {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	c.mu.Lock()
	*sink = append(*sink, message{Method: r.Method, Path: r.URL.Path, Host: r.Host, Auth: r.Header.Get("Authorization"), Query: r.URL.RawQuery, Body: body})
	c.mu.Unlock()
	return body
}

// sendEnvelope writes one framed Connect streaming message.
func sendEnvelope(w http.ResponseWriter, payload []byte) {
	var header [5]byte
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	_, _ = w.Write(header[:])
	_, _ = w.Write(payload)
}

func sendEvent(w http.ResponseWriter, event map[string]any) {
	raw, _ := json.Marshal(map[string]any{"event": event})
	sendEnvelope(w, raw)
}

func sendEndStream(w http.ResponseWriter, code string) {
	body := map[string]any{}
	if code != "" {
		body["error"] = map[string]any{"code": code, "message": "synthetic stream failure"}
	}
	raw, _ := json.Marshal(body)
	var header [5]byte
	header[0] = connectEndStreamFlag
	binary.BigEndian.PutUint32(header[1:], uint32(len(raw)))
	_, _ = w.Write(header[:])
	_, _ = w.Write(raw)
}

func encode(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

// startStream is the vendor-shaped process stream: start, one output event, end.
// The default stream exits zero with no output; tests override it for exit codes,
// oversized output and truncated streams.
func writeStartStream(w http.ResponseWriter, exitCode int, stdout, stderr string) {
	w.Header().Set("Content-Type", connectContentType)
	w.WriteHeader(http.StatusOK)
	sendEvent(w, map[string]any{"start": map[string]any{"pid": 4242}})
	sendEvent(w, map[string]any{"data": map[string]any{"stdout": encode(stdout), "stderr": encode(stderr)}})
	sendEvent(w, map[string]any{"end": map[string]any{"exitCode": exitCode, "exited": true, "status": "exit status " + strconv.Itoa(exitCode)}})
	sendEndStream(w, "")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (c *cluster) has(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.sandboxes {
		if entry["sandboxID"] == id {
			return true
		}
	}
	return false
}

func (c *cluster) list(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if c.listOverride != nil {
		// The scripted response is returned verbatim, as a server that filtered
		// loosely or lied would. The adapter must still defend itself.
		_ = json.NewEncoder(w).Encode(c.listOverride)
		return
	}
	c.mu.Lock()
	entries := append([]map[string]any(nil), c.sandboxes...)
	c.mu.Unlock()
	pairs := map[string]string{}
	for _, pair := range strings.Split(r.URL.Query().Get("metadata"), "&") {
		if key, value, ok := strings.Cut(pair, "="); ok {
			pairs[key] = value
		}
	}
	matched := []map[string]any{}
	for _, entry := range entries {
		metadata := entryMetadata(entry)
		ok := true
		for key, value := range pairs {
			if metadata[key] != value {
				ok = false
				break
			}
		}
		if ok {
			matched = append(matched, entry)
		}
	}
	_ = json.NewEncoder(w).Encode(matched)
}

func entryMetadata(entry map[string]any) map[string]string {
	if metadata, ok := entry["metadata"].(map[string]string); ok {
		return metadata
	}
	metadata := map[string]string{}
	if generic, ok := entry["metadata"].(map[string]any); ok {
		for key, value := range generic {
			if text, ok := value.(string); ok {
				metadata[key] = text
			}
		}
	}
	return metadata
}

func (c *cluster) createSandbox(w http.ResponseWriter, body []byte) {
	var request map[string]any
	if json.Unmarshal(body, &request) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if c.createStatus != 0 {
		w.WriteHeader(c.createStatus)
		// A hostile or buggy server can echo secrets in an error body. The
		// adapter must never surface it.
		_, _ = w.Write([]byte(`{"detail":"rejected key=` + testKey + ` credential=` + testCred + `"}`))
		return
	}
	metadata := map[string]string{}
	if raw, ok := request["metadata"].(map[string]any); ok {
		for key, value := range raw {
			if text, ok := value.(string); ok {
				metadata[key] = text
			}
		}
	}
	id := c.addSandbox(metadata, "running")
	w.WriteHeader(http.StatusCreated)
	if c.createBody != "" {
		_, _ = w.Write([]byte(c.createBody))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sandboxID": id, "templateID": "tpl-pinned", "clientID": "core", "envdVersion": "0.2.0",
		"domain": c.domain, "envdAccessToken": testToken,
	})
}

func (c *cluster) detail(w http.ResponseWriter, id string) {
	if c.detailOverride != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(c.detailOverride)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.sandboxes {
		if entry["sandboxID"] == id {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(entry)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"synthetic not found"}`))
}

func (c *cluster) kill(w http.ResponseWriter, id string) {
	if c.deleteStatus != 0 {
		w.WriteHeader(c.deleteStatus)
		return
	}
	if !c.has(id) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !c.deleteKeeps {
		c.remove(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveControl impersonates the pinned CubeAPI surface.
func (c *cluster) serveControl(w http.ResponseWriter, r *http.Request) {
	body := c.record(&c.controlCalls, r)
	if c.redirect {
		http.Redirect(w, r, "/cubeapi/v1/health", http.StatusFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/cubeapi/v1")
	switch {
	case path == "/health" && r.Method == http.MethodGet:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	case path == "/sandboxes" && r.Method == http.MethodGet:
		c.list(w, r)
	case path == "/sandboxes" && r.Method == http.MethodPost:
		c.createSandbox(w, body)
	case strings.HasPrefix(path, "/sandboxes/") && strings.HasSuffix(path, "/refreshes") && r.Method == http.MethodPost:
		if c.refreshStatus != 0 {
			w.WriteHeader(c.refreshStatus)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(path, "/sandboxes/") && r.Method == http.MethodGet:
		c.detail(w, strings.TrimPrefix(path, "/sandboxes/"))
	case strings.HasPrefix(path, "/sandboxes/") && r.Method == http.MethodDelete:
		c.kill(w, strings.TrimPrefix(path, "/sandboxes/"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// serveData impersonates CubeProxy in front of envd and the Runtime readiness
// endpoint. Every request must carry the virtual sandbox Host header.
func (c *cluster) serveData(w http.ResponseWriter, r *http.Request) {
	_ = c.record(&c.dataCalls, r)
	if c.redirect {
		http.Redirect(w, r, "/healthz", http.StatusFound)
		return
	}
	switch r.URL.Path {
	case readinessPath:
		w.WriteHeader(c.readiness)
		if c.readiness < 300 {
			_, _ = w.Write([]byte(`{"ready":true}`))
		}
	case "/process.Process/Start":
		if c.stream != nil {
			c.stream(w)
			return
		}
		writeStartStream(w, 0, "", "")
	case "/process.Process/SendInput", "/process.Process/CloseStdin", "/files", "/filesystem.Filesystem/Stat":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
