package opensandbox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// fake is an OpenSandbox server: the lifecycle routes and the execd routes
// behind the server proxy. It records every call and injects one fault.
type fake struct {
	t       *testing.T
	server  *httptest.Server
	mu      sync.Mutex
	calls   []string
	items   map[string]map[string]any // id -> sandbox
	next    int
	state   string // native state of created sandboxes
	mode    string // enforcementMode
	fault   func(call string, w http.ResponseWriter) bool
	keepOn  bool // DELETE succeeds without removing
	created map[string]any
	// execd
	uploads  map[string]uploaded
	commands [][]string
	cwds     []string
	exit     func(argv []string) (stdout, stderr string, exit *int)
	metrics  map[string]any
}

type uploaded struct {
	Mode int
	Data string
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, items: map[string]map[string]any{}, state: "Running", mode: "dns+nft", uploads: map[string]uploaded{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fake) record(call string) { f.calls = append(f.calls, call) }

func (f *fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fake) add(r sandbox.Reference, installation string, extra map[string]string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addLocked(map[string]string{keyInstallation: installation, keyTenant: r.TenantID, keyEnvironment: r.EnvironmentID, keyAllocation: r.AllocationID}, extra)
}

func (f *fake) addLocked(metadata map[string]string, extra map[string]string) string {
	f.next++
	id := "sb-" + string(rune('a'+f.next))
	for k, v := range extra {
		metadata[k] = v
	}
	f.items[id] = map[string]any{
		"id": id, "image": map[string]any{"uri": "runtime:test"}, "status": map[string]any{"state": f.state},
		"metadata": metadata, "createdAt": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), "entrypoint": []string{"sleep"},
	}
	return id
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v1/sandboxes")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	id := parts[0]
	call := ""
	switch {
	case path == "" && r.Method == http.MethodGet:
		call = "list"
	case path == "" && r.Method == http.MethodPost:
		call = "create"
	case len(parts) == 1 && r.Method == http.MethodGet:
		call = "get"
	case len(parts) == 1 && r.Method == http.MethodDelete:
		call = "delete"
	case len(parts) == 2 && parts[1] == "metadata":
		call = "patch"
	case len(parts) == 2 && parts[1] == "renew-expiration":
		call = "renew"
	case len(parts) == 2 && parts[1] == "networkpolicy":
		call = "networkpolicy"
	case len(parts) == 3 && parts[1] == "endpoints":
		call = "endpoint"
	case len(parts) >= 4 && parts[1] == "proxy":
		call = "execd" + strings.TrimPrefix(path, "/"+id+"/proxy/44772")
	}
	f.record(call)
	if f.fault != nil && f.fault(call, w) {
		return
	}
	switch call {
	case "list":
		want, _ := url.ParseQuery(r.URL.Query().Get("metadata"))
		var items []map[string]any
		for _, item := range f.items {
			match := true
			for k := range want {
				match = match && item["metadata"].(map[string]string)[k] == want.Get(k)
			}
			if match {
				items = append(items, item)
			}
		}
		writeJSON(w, 200, map[string]any{"items": items, "pagination": map[string]any{"page": 1, "pageSize": 100, "totalItems": len(items), "totalPages": 1, "hasNextPage": false}})
	case "create":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = body
		metadata := map[string]string{}
		for k, v := range body["metadata"].(map[string]any) {
			metadata[k] = v.(string)
		}
		newID := f.addLocked(metadata, nil)
		writeJSON(w, 201, f.items[newID])
	case "get":
		if item, ok := f.items[id]; ok {
			writeJSON(w, 200, item)
			return
		}
		writeJSON(w, 404, map[string]string{"code": "SANDBOX::NOT_FOUND", "message": "missing"})
	case "delete":
		if !f.keepOn {
			delete(f.items, id)
		}
		w.WriteHeader(204)
	case "patch":
		var patch map[string]string
		_ = json.NewDecoder(r.Body).Decode(&patch)
		for k, v := range patch {
			f.items[id]["metadata"].(map[string]string)[k] = v
		}
		writeJSON(w, 200, f.items[id])
	case "renew":
		writeJSON(w, 200, map[string]any{"expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	case "networkpolicy":
		writeJSON(w, 200, map[string]any{"status": "ok", "enforcementMode": f.mode})
	case "endpoint":
		writeJSON(w, 200, map[string]any{"endpoint": strings.TrimPrefix(f.server.URL, "http://") + "/v1/sandboxes/" + id + "/proxy/44772"})
	case "execd/ping":
		w.WriteHeader(200)
	case "execd/files/upload":
		f.upload(w, r)
	case "execd/command":
		f.command(w, r)
	case "execd/metrics":
		writeJSON(w, 200, f.metrics)
	default:
		writeJSON(w, 404, map[string]string{"code": "NOT_FOUND", "message": r.Method + " " + r.URL.Path})
	}
}

func (f *fake) upload(w http.ResponseWriter, r *http.Request) {
	reader, err := r.MultipartReader()
	if err != nil {
		f.t.Fatal(err)
	}
	var meta struct {
		Path string
		Mode int
	}
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(part)
		if part.FormName() == "metadata" {
			_ = json.Unmarshal(data, &meta)
		} else {
			f.uploads[meta.Path] = uploaded{meta.Mode, string(data)}
		}
	}
	w.WriteHeader(200)
}

func (f *fake) command(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Argv []string
		Cwd  string
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.commands, f.cwds = append(f.commands, body.Argv), append(f.cwds, body.Cwd)
	stdout, stderr, exit := "", "", new(int)
	if f.exit != nil {
		stdout, stderr, exit = f.exit(body.Argv)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	event := func(v map[string]any) {
		raw, _ := json.Marshal(v)
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
	}
	event(map[string]any{"type": "init", "text": "c1"})
	if stdout != "" {
		event(map[string]any{"type": "stdout", "text": stdout})
	}
	if stderr != "" {
		event(map[string]any{"type": "stderr", "text": stderr})
	}
	switch {
	case exit == nil:
	case *exit == 0:
		event(map[string]any{"type": "execution_complete", "exit_code": 0})
	default:
		event(map[string]any{"type": "error", "error": map[string]any{"ename": "CommandExecError", "evalue": "x", "exit_code": *exit}})
	}
}

func testProvider(t *testing.T, f *fake) (*Provider, sandbox.Reference) {
	t.Helper()
	p, err := newProvider(testConfig(f.server.URL))
	if err != nil {
		t.Fatal(err)
	}
	p.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("203.0.113.5")}, nil
	}
	return p, sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
}

func testConfig(apiURL string) sandbox.DirectConfig {
	return sandbox.DirectConfig{InstallationID: uuid.NewString(), Selection: sandbox.Selection{
		Provider:       "opensandbox",
		DeploymentSpec: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 4096}, Network: sandbox.DefaultNetwork()},
		Configuration:  &DeploymentConfiguration{APIURL: apiURL, Image: "runtime:test", APIKey: "synthetic-key"},
	}}
}

func bootstrap(r sandbox.Reference) sandbox.Bootstrap {
	return sandbox.Bootstrap{Reference: r, DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1", Credential: "synthetic",
		Egress: sandbox.Egress{Public: true, PrivateDomains: []string{}, Domains: []string{}, Endpoints: []string{}}}
}

func bounded(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}
