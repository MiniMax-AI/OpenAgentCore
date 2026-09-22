package cubesandbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Every operator configuration branch the adapter refuses.
func TestProviderRejectsUnsafeOperatorConfiguration(t *testing.T) {
	base := Config{
		InstallationID: uuid.NewString(), APIURL: "https://cubeapi.internal/cubeapi/v1", ProxyNodeIP: "10.0.0.20",
		SandboxDomain: "cube.app", ProxyScheme: "https", Template: "tpl-pinned", APIKey: "synthetic", LeaseSeconds: 43200, HostMountRoot: "/data/shared/parsar",
	}
	for name, change := range map[string]func(*Config){
		"installation not a canonical uuid": func(c *Config) { c.InstallationID = "not-a-uuid" },
		"nil uuid installation":             func(c *Config) { c.InstallationID = uuid.Nil.String() },
		"missing api url":                   func(c *Config) { c.APIURL = "" },
		"api url with credentials":          func(c *Config) { c.APIURL = "https://user:pass@cubeapi.internal/cubeapi/v1" },
		"api url with a query":              func(c *Config) { c.APIURL = "https://cubeapi.internal/cubeapi/v1?token=1" },
		"relative api url":                  func(c *Config) { c.APIURL = "/cubeapi/v1" },
		"missing template":                  func(c *Config) { c.Template = " " },
		"missing api key":                   func(c *Config) { c.APIKey = "" },
		"missing sandbox domain":            func(c *Config) { c.SandboxDomain = "" },
		"unsupported proxy scheme":          func(c *Config) { c.ProxyScheme = "ftp" },
		"lease below the floor":             func(c *Config) { c.LeaseSeconds = 3599 },
		"lease above the ceiling":           func(c *Config) { c.LeaseSeconds = 86401 },
		"host root is the host root":        func(c *Config) { c.HostMountRoot = "/" },
		"host root is relative":             func(c *Config) { c.HostMountRoot = "data/shared" },
		"host root with traversal":          func(c *Config) { c.HostMountRoot = "/data/../shared" },
		"proxy node with a path":            func(c *Config) { c.ProxyNodeIP = "10.0.0.20/lan" },
		"proxy node with a bad port":        func(c *Config) { c.ProxyNodeIP = "10.0.0.20:70000" },
		"egress entry with whitespace":      func(c *Config) { c.PlatformEgress = []string{"10.0.0.20 10.0.0.21"} },
		"egress entry with a scheme":        func(c *Config) { c.PlatformEgress = []string{"https://models.internal"} },
	} {
		candidate := base
		change(&candidate)
		if _, err := New(candidate); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatalf("%s: accepted invalid operator configuration (%v)", name, err)
		}
	}
	if _, err := New(base); err != nil {
		t.Fatalf("valid operator configuration rejected: %v", err)
	}
}

// The create request must carry the pinned template, the ownership metadata, the
// Core TTL and the per-allocation host mounts, and nothing secret.
func TestCreateUsesPinnedTemplateMetadataStorageAndTtl(t *testing.T) {
	cluster := newCluster(t)
	installation := uuid.NewString()
	provider := cluster.provider(installation)
	bootstrap := cluster.bootstrap()
	info, err := provider.Create(testContext(t), bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if info.ProviderID == "" || info.State != "running" || !info.BootstrapComplete || info.Reference != bootstrap.Reference {
		t.Fatalf("create did not report observed readiness: %+v", info)
	}
	var created map[string]any
	posts := 0
	for _, request := range cluster.controlRequests() {
		if request.Method == "POST" && strings.HasSuffix(request.Path, "/sandboxes") {
			posts++
			if request.Auth != "Bearer "+testKey {
				t.Fatalf("control credential header missing: %q", request.Auth)
			}
			if json.Unmarshal(request.Body, &created) != nil {
				t.Fatal("create body was not JSON")
			}
		}
	}
	if posts != 1 {
		t.Fatalf("expected exactly one create request, saw %d", posts)
	}
	if created["templateID"] != "tpl-pinned" || created["timeout"] != float64(43200) {
		t.Fatalf("pinned template or TTL missing: %v", created)
	}
	metadata, ok := created["metadata"].(map[string]any)
	if !ok {
		t.Fatal("create metadata missing")
	}
	for key, value := range cluster.metadata(installation, bootstrap.Reference) {
		if metadata[key] != value {
			t.Fatalf("ownership metadata %q lost: %v", key, metadata)
		}
	}
	raw, ok := metadata[hostMountKey].(string)
	if !ok {
		t.Fatalf("host mount descriptor missing: %v", metadata)
	}
	var mounts []map[string]any
	if json.Unmarshal([]byte(raw), &mounts) != nil || len(mounts) != 2 {
		t.Fatalf("host mount descriptor malformed: %q", raw)
	}
	expectedRoot := "/data/shared/parsar/" + installation + "/" + bootstrap.TenantID + "/" + bootstrap.EnvironmentID + "/" + bootstrap.AllocationID
	if mounts[0]["hostPath"] != expectedRoot || mounts[0]["mountPath"] != environmentMount || mounts[0]["readOnly"] != false {
		t.Fatalf("environment mount is not the derived per-allocation store: %v", mounts[0])
	}
	if mounts[1]["hostPath"] != expectedRoot+"/workspace" || mounts[1]["mountPath"] != workspaceMount {
		t.Fatalf("workspace mount is not the second view of the same store: %v", mounts[1])
	}
	if created["allow_internet_access"] != nil || created["network"] != nil {
		t.Fatal("an enabled Session must not install an egress restriction")
	}
	// The credential travels in a file request, never in argv or metadata.
	for _, request := range cluster.dataRequests() {
		if request.Path == "/process.Process/Start" && strings.Contains(string(request.Body), testCred) {
			t.Fatal("credential reached a process argument")
		}
	}
	if strings.Contains(string(mustJSON(t, created)), testCred) {
		t.Fatal("credential reached the create request")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The data plane is addressed by virtual Host through the proxy node.
func TestDataPlaneUsesVirtualHostForBothPorts(t *testing.T) {
	cluster := newCluster(t)
	provider := cluster.provider(uuid.NewString())
	bootstrap := cluster.bootstrap()
	info, err := provider.Create(testContext(t), bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string]bool{}
	for _, request := range cluster.dataRequests() {
		hosts[request.Host] = true
	}
	if !hosts["49983-"+info.ProviderID+"."+cluster.domain] {
		t.Fatalf("envd requests missed the virtual sandbox host: %v", hosts)
	}
	if !hosts["49984-"+info.ProviderID+"."+cluster.domain] {
		t.Fatalf("readiness probe missed the virtual sandbox host: %v", hosts)
	}
}

// Invalid Session input must be refused before any cluster request.
func TestCreateRejectsInvalidBootstrapBeforeAnyRequest(t *testing.T) {
	cluster := newCluster(t)
	provider := cluster.provider(uuid.NewString())
	base := cluster.bootstrap()
	for name, change := range map[string]func(*sandbox.Bootstrap){
		"empty credential":       func(b *sandbox.Bootstrap) { b.Credential = " " },
		"core url with userinfo": func(b *sandbox.Bootstrap) { b.CoreURL = "http://user:pass@core.invalid/api/v1" },
		"core url without host":  func(b *sandbox.Bootstrap) { b.CoreURL = "http:///api/v1" },
		"core url unsupported":   func(b *sandbox.Bootstrap) { b.CoreURL = "file:///api/v1" },
		"unknown network access": func(b *sandbox.Bootstrap) { b.NetworkAccess = "restricted" },
		"allowed domains":        func(b *sandbox.Bootstrap) { b.AllowedDomains = []string{"example.com"} },
		"missing session":        func(b *sandbox.Bootstrap) { b.SessionID = "" },
		"missing device":         func(b *sandbox.Bootstrap) { b.DeviceID = "" },
		"non-uuid tenant":        func(b *sandbox.Bootstrap) { b.TenantID = "tenant" },
	} {
		candidate := base
		change(&candidate)
		if _, err := provider.Create(testContext(t), candidate); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatalf("%s: invalid bootstrap reached the cluster (%v)", name, err)
		}
	}
	if len(cluster.controlRequests()) != 0 || len(cluster.dataRequests()) != 0 {
		t.Fatalf("invalid bootstrap produced cluster traffic: %d/%d", len(cluster.controlRequests()), len(cluster.dataRequests()))
	}
	// An unspecified network access value is accepted and means the platform
	// default, which is what the current hosted profile sends.
	permitted := base
	permitted.NetworkAccess = ""
	if _, err := provider.Create(testContext(t), permitted); err != nil {
		t.Fatalf("unspecified network access rejected: %v", err)
	}
}

// An existing allocation is never overwritten, and the adapter does not send a
// second create request.
func TestCreateReportsExistingAllocationWithoutASecondCreate(t *testing.T) {
	cluster := newCluster(t)
	installation := uuid.NewString()
	provider := cluster.provider(installation)
	bootstrap := cluster.bootstrap()
	cluster.addSandbox(cluster.metadata(installation, bootstrap.Reference), "running")
	existing, err := provider.Create(testContext(t), bootstrap)
	if !errors.Is(err, sandbox.ErrExists) {
		t.Fatalf("existing allocation not reported: %v", err)
	}
	if existing.ProviderID == "" || existing.State != "running" {
		t.Fatalf("observed state lost: %+v", existing)
	}
	if posts := creates(cluster.controlRequests()); posts != 0 {
		t.Fatalf("create was replayed %d times", posts)
	}
}

// A sandbox whose metadata is not this allocation's is never adopted.
func TestCreateRefusesForeignOwnershipMatch(t *testing.T) {
	cluster := newCluster(t)
	installation := uuid.NewString()
	provider := cluster.provider(installation)
	bootstrap := cluster.bootstrap()
	foreign := cluster.metadata(installation, bootstrap.Reference)
	foreign[labelPrefix+"tenant"] = uuid.NewString()
	cluster.listOverride = []map[string]any{{"sandboxID": "sbx-foreign", "state": "running", "metadata": foreign}}
	if _, err := provider.Create(testContext(t), bootstrap); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("foreign ownership accepted: %v", err)
	}
	if posts := creates(cluster.controlRequests()); posts != 0 {
		t.Fatalf("foreign match triggered %d creates", posts)
	}
}

// A lost create response leaves owner state uncertain. The caller keeps the
// reference and reconciles with GetInfo; nothing is replayed.
func TestCreateReturnsReferenceWhenTheResponseIsLost(t *testing.T) {
	cluster := newCluster(t)
	installation := uuid.NewString()
	provider := cluster.provider(installation)
	bootstrap := cluster.bootstrap()
	cluster.createBody = "not-json"
	info, err := provider.Create(testContext(t), bootstrap)
	if err == nil {
		t.Fatal("lost create response reported as success")
	}
	if info.Reference != bootstrap.Reference || info.ProviderID != "" {
		t.Fatalf("uncertain owner state was erased: %+v", info)
	}
	if posts := creates(cluster.controlRequests()); posts != 1 {
		t.Fatalf("expected exactly one create attempt, saw %d", posts)
	}
	observed, err := provider.GetInfo(testContext(t), bootstrap.Reference)
	if err != nil || observed.ProviderID == "" || observed.State != "running" {
		t.Fatalf("reconciliation did not resolve the allocation: %+v %v", observed, err)
	}
	if _, err := provider.Create(testContext(t), bootstrap); !errors.Is(err, sandbox.ErrExists) {
		t.Fatalf("second create did not report the existing allocation: %v", err)
	}
	if posts := creates(cluster.controlRequests()); posts != 1 {
		t.Fatalf("create was replayed after reconciliation: %d", posts)
	}
}

// A disabled Session denies ordinary egress but keeps Core and the configured
// platform addresses reachable, because the daemon and the harness share the
// interface.
func TestDisabledNetworkPolicyKeepsCoreAndPlatformReachable(t *testing.T) {
	cluster := newCluster(t)
	provider := cluster.provider(uuid.NewString(), func(c *Config) { c.PlatformEgress = []string{"models.internal", "10.1.0.0/16"} })
	bootstrap := cluster.bootstrap()
	bootstrap.NetworkAccess = "disabled"
	if _, err := provider.Create(testContext(t), bootstrap); err != nil {
		t.Fatal(err)
	}
	var created map[string]any
	for _, request := range cluster.controlRequests() {
		if request.Method == "POST" && strings.HasSuffix(request.Path, "/sandboxes") {
			if json.Unmarshal(request.Body, &created) != nil {
				t.Fatal("create body was not JSON")
			}
		}
	}
	if created["allow_internet_access"] != false {
		t.Fatalf("disabled Session did not disable public egress: %v", created["allow_internet_access"])
	}
	network, ok := created["network"].(map[string]any)
	if !ok {
		t.Fatal("disabled Session did not install an egress policy")
	}
	allow, _ := network["allowOut"].([]any)
	joined := ""
	for _, entry := range allow {
		text, _ := entry.(string)
		joined += text + " "
	}
	for _, expected := range []string{"core.invalid", "models.internal", "10.1.0.0/16"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("trusted address %q lost from the egress policy: %s", expected, joined)
		}
	}
}

// Read-path ownership: zero matches is absence, two matches is an anomaly, and
// readiness evidence decides BootstrapComplete.
func TestGetInfoOwnershipAndReadinessEvidence(t *testing.T) {
	cluster := newCluster(t)
	installation := uuid.NewString()
	provider := cluster.provider(installation)
	bootstrap := cluster.bootstrap()
	if _, err := provider.GetInfo(testContext(t), bootstrap.Reference); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("absent allocation not reported as absent: %v", err)
	}

	metadata := cluster.metadata(installation, bootstrap.Reference)
	cluster.addSandbox(metadata, "running")
	cluster.addSandbox(metadata, "running")
	if _, err := provider.GetInfo(testContext(t), bootstrap.Reference); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("duplicate ownership accepted: %v", err)
	}

	cluster.listOverride = nil
	cluster.sandboxes = nil
	id := cluster.addSandbox(metadata, "running")

	// A sandbox that has not authenticated its daemon is running but not ready.
	cluster.readiness = 503
	info, err := provider.GetInfo(testContext(t), bootstrap.Reference)
	if err != nil || info.State != "running" || info.BootstrapComplete {
		t.Fatalf("unready sandbox misreported: %+v %v", info, err)
	}
	cluster.readiness = 200
	info, err = provider.GetInfo(testContext(t), bootstrap.Reference)
	if err != nil || info.State != "running" || !info.BootstrapComplete || info.ProviderID != id {
		t.Fatalf("ready sandbox misreported: %+v %v", info, err)
	}

	// Only the pinned state vocabulary is admitted; anything else is unknown and
	// never running, and a paused sandbox is never reported as ready.
	for state, expected := range map[string]string{"paused": "paused", "pausing": "pausing", "terminated": "unknown", "": "unknown"} {
		cluster.setState(id, state)
		info, err := provider.GetInfo(testContext(t), bootstrap.Reference)
		if err != nil || info.State != expected || info.BootstrapComplete {
			t.Fatalf("state %q mapped to %q with readiness %v (%v)", state, info.State, info.BootstrapComplete, err)
		}
	}

	// A foreign allocation is not visible to this provider at all.
	other := bootstrap.Reference
	other.TenantID = uuid.NewString()
	if _, err := provider.GetInfo(testContext(t), other); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("foreign allocation visible: %v", err)
	}
}

// Renew extends the TTL of the original running sandbox and never revives one.
func TestRenewExtendsOnlyARunningSandbox(t *testing.T) {
	cluster := newCluster(t)
	installation := uuid.NewString()
	provider := cluster.provider(installation)
	bootstrap := cluster.bootstrap()
	id := cluster.addSandbox(cluster.metadata(installation, bootstrap.Reference), "paused")
	if _, err := provider.Renew(testContext(t), bootstrap.Reference); err == nil {
		t.Fatal("paused allocation renewed")
	}
	if calls := refreshes(cluster.controlRequests()); calls != 0 {
		t.Fatalf("a TTL call was issued for a stopped sandbox: %d", calls)
	}
	cluster.setState(id, "running")
	info, err := provider.Renew(testContext(t), bootstrap.Reference)
	if err != nil || info.State != "running" || !info.BootstrapComplete || info.Reference != bootstrap.Reference {
		t.Fatalf("renewed allocation misreported: %+v %v", info, err)
	}
	var body map[string]any
	for _, request := range cluster.controlRequests() {
		if request.Method == "POST" && strings.HasSuffix(request.Path, "/refreshes") {
			if json.Unmarshal(request.Body, &body) != nil {
				t.Fatal("refresh body was not JSON")
			}
		}
	}
	if body["duration"] != float64(43200) {
		t.Fatalf("refresh did not carry the pinned lease: %v", body)
	}
}

// Kill verifies every owner before deleting any, is idempotent for absence, and
// refuses to report an unconfirmed removal as success.
func TestKillVerifiesOwnersAndConfirmsRemoval(t *testing.T) {
	cluster := newCluster(t)
	installation := uuid.NewString()
	provider := cluster.provider(installation)
	bootstrap := cluster.bootstrap()

	// A foreign sandbox in the response stops the operation before any delete.
	foreign := cluster.metadata(installation, bootstrap.Reference)
	foreign[labelPrefix+"environment"] = uuid.NewString()
	cluster.listOverride = []map[string]any{{"sandboxID": "sbx-foreign", "state": "running", "metadata": foreign}}
	if err := provider.Kill(testContext(t), bootstrap.Reference); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("foreign ownership accepted during cleanup: %v", err)
	}
	if calls := deletes(cluster.controlRequests()); calls != 0 {
		t.Fatalf("foreign sandbox was deleted: %d", calls)
	}

	// A candidate the vendor cannot identify is ambiguous state, never a
	// path-derived delete target.
	before := deletes(cluster.controlRequests())
	cluster.listOverride = []map[string]any{{"sandboxID": "", "state": "running", "metadata": cluster.metadata(installation, bootstrap.Reference)}}
	if err := provider.Kill(testContext(t), bootstrap.Reference); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("an unidentified candidate was accepted: %v", err)
	}
	if after := deletes(cluster.controlRequests()); after != before {
		t.Fatalf("a delete was issued for an unidentified candidate: %d", after-before)
	}
	cluster.listOverride = nil

	// A detail response that does not match the requested identity is refused.
	cluster.listOverride = nil
	cluster.detailOverride = map[string]any{"sandboxID": "sbx-other", "state": "running", "metadata": cluster.metadata(installation, bootstrap.Reference)}
	cluster.addSandbox(cluster.metadata(installation, bootstrap.Reference), "running")
	if err := provider.Kill(testContext(t), bootstrap.Reference); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("mismatched detail identity accepted: %v", err)
	}
	if calls := deletes(cluster.controlRequests()); calls != 0 {
		t.Fatalf("mismatched sandbox was deleted: %d", calls)
	}
	cluster.detailOverride = nil

	// The happy path removes every owned match and confirms absence.
	second := cluster.addSandbox(cluster.metadata(installation, bootstrap.Reference), "running")
	if err := provider.Kill(testContext(t), bootstrap.Reference); err != nil {
		t.Fatal(err)
	}
	if cluster.has(second) {
		t.Fatal("owned sandbox survived cleanup")
	}
	if err := provider.Kill(testContext(t), bootstrap.Reference); err != nil {
		t.Fatalf("repeated cleanup was not a no-op success: %v", err)
	}
	if calls := deletes(cluster.controlRequests()); calls == 0 {
		t.Fatal("no delete request was recorded")
	}

	// A delete that does not actually remove the sandbox is a failure.
	cluster.addSandbox(cluster.metadata(installation, bootstrap.Reference), "running")
	cluster.deleteKeeps = true
	err := provider.Kill(testContext(t), bootstrap.Reference)
	if err == nil || !strings.Contains(err.Error(), "unconfirmed") {
		t.Fatalf("unconfirmed removal reported as success: %v", err)
	}

	// The pinned delete failures are never treated as success.
	cluster.deleteKeeps = false
	cluster.reset()
	for _, status := range []int{408, 409, 503} {
		if err := provider.Kill(testContext(t), bootstrap.Reference); err != nil {
			// Nothing to delete is a no-op success.
			t.Fatalf("HTTP %d check could not start clean: %v", status, err)
		}
		cluster.addSandbox(cluster.metadata(installation, bootstrap.Reference), "running")
		cluster.deleteStatus = status
		if err := provider.Kill(testContext(t), bootstrap.Reference); err == nil {
			t.Fatalf("HTTP %d on delete reported as success", status)
		}
		cluster.deleteStatus = 0
		cluster.reset()
	}
}

// A redirect is rejected rather than followed, on both planes. The data-plane
// case is separate because a control-plane failure short-circuits GetInfo.
func TestRedirectsAreRejected(t *testing.T) {
	cluster := newCluster(t)
	installation := uuid.NewString()
	provider := cluster.provider(installation)
	bootstrap := cluster.bootstrap()
	ctx := testContext(t)
	cluster.redirect = true
	if err := provider.Health(ctx); err == nil {
		t.Fatal("control-plane redirect was followed")
	}
	if _, err := provider.Create(ctx, bootstrap); err == nil {
		t.Fatal("redirected create was accepted")
	}
	if len(cluster.controlRequests()) == 0 {
		t.Fatal("control-plane request was not recorded")
	}
	if _, err := provider.GetInfo(ctx, bootstrap.Reference); err == nil {
		t.Fatal("redirected read was accepted")
	}

	// A redirect from the data plane is rejected too, and the readiness probe
	// never follows it to the redirect target.
	cluster.redirect = false
	cluster.dataRedirect = true
	cluster.addSandbox(cluster.metadata(installation, bootstrap.Reference), "running")
	info, err := provider.GetInfo(ctx, bootstrap.Reference)
	if err != nil {
		t.Fatalf("a redirected readiness probe reported a provider error: %v", err)
	}
	if info.State != "running" || info.BootstrapComplete {
		t.Fatalf("redirected readiness misreported readiness: %+v", info)
	}
	probes := 0
	for _, request := range cluster.dataRequests() {
		if request.Path == readinessPath {
			probes++
		}
	}
	if probes != 1 {
		t.Fatalf("the data-plane redirect was followed %d times", probes-1)
	}
}

// The vendor can process a create and still lose the answer at the transport
// level. The caller keeps the reference and reconciles; nothing is replayed.
func TestCreateSurvivesADroppedConnectionAfterTheServerCreatedTheSandbox(t *testing.T) {
	cluster := newCluster(t)
	installation := uuid.NewString()
	provider := cluster.provider(installation)
	bootstrap := cluster.bootstrap()
	cluster.createDrop = true
	info, err := provider.Create(testContext(t), bootstrap)
	if err == nil {
		t.Fatal("a dropped create response reported as success")
	}
	if info.Reference != bootstrap.Reference {
		t.Fatalf("uncertain owner state was erased: %+v", info)
	}
	observed, err := provider.GetInfo(testContext(t), bootstrap.Reference)
	if err != nil || observed.ProviderID == "" || !observed.BootstrapComplete {
		t.Fatalf("reconciliation did not resolve the created sandbox: %+v %v", observed, err)
	}
	if posts := creates(cluster.controlRequests()); posts != 1 {
		t.Fatalf("the dropped response caused %d create requests", posts)
	}
}

// No error text may carry the API key, the executor credential, the access token
// or a response body.
func TestErrorTextCarriesNoCredentialOrResponseBody(t *testing.T) {
	cluster := newCluster(t)
	provider := cluster.provider(uuid.NewString())
	bootstrap := cluster.bootstrap()
	ctx := testContext(t)
	cluster.createStatus = 503
	_, err := provider.Create(ctx, bootstrap)
	if err == nil {
		t.Fatal("rejected create reported as success")
	}
	for _, secret := range []string{testKey, testCred, testToken, "rejected key"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error text leaked %q: %v", secret, err)
		}
	}
	cluster.createStatus = 400
	if _, err := provider.Create(ctx, bootstrap); err == nil || errors.Is(err, sandbox.ErrInvalid) == false {
		t.Fatalf("invalid create was not classified as invalid input: %v", err)
	}
	// A transport failure must not echo the request either.
	cluster.control.Close()
	if err := provider.Health(ctx); err == nil {
		t.Fatal("unreachable control plane reported as healthy")
	}
	if _, err := provider.GetInfo(ctx, bootstrap.Reference); err == nil {
		t.Fatal("unreachable control plane reported an allocation")
	}
}
