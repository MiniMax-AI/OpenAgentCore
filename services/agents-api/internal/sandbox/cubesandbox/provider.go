// Package cubesandbox implements compute lifecycle for a colocated Runtime that
// runs as a microVM on an operator-configured CubeSandbox cluster instead of a
// local Docker container. It does not implement model execution, public Files,
// scheduling, Turns, cancellation or expiry; Core keeps all of that.
//
// The package depends on the vendor's pinned HTTP and Connect-JSON contract and
// nothing else: see contract/README.md for the pinned revisions, hashes and the
// client-side facts taken from the vendor's own SDK at that revision. Adding a
// sandbox SDK to this module graph for an opt-in backend is not justified.
//
// The daemon auth profile is written through envd's file API and its mode is
// then verified with a trusted command. Command input travels over the pinned
// process protocol's SendInput/CloseStdin operations, so confidential
// initialization payloads never reach argv, metadata, environment variables or
// an image layer.
package cubesandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

const (
	// ReadinessPort is the fixed in-sandbox port of the Runtime readiness
	// endpoint, which is also the Cube template probe (specification §8.1). It is
	// a package constant rather than operator configuration because the image,
	// the template and this adapter must agree on it.
	ReadinessPort = 49984
	// envdPort is the vendor's fixed envd port.
	envdPort = 49983
	// runtimeUser owns every Runtime path. Initialization never runs as root.
	runtimeUser = "runtime"

	labelPrefix  = "io.parsar.agents-api."
	hostMountKey = "host-mount"

	// controlResponseBytes bounds a control-plane response body.
	controlResponseBytes = 1 << 20
	// controlTimeout bounds one control-plane request that has no shorter caller
	// deadline. Data-plane streams are bounded by the caller's deadline only.
	controlTimeout = 5 * time.Minute

	// minLeaseSeconds and maxLeaseSeconds bound the Cube idle TTL. Core's
	// one-hour lease stays authoritative; the Cube TTL is a safety net that must
	// outlive Core's window.
	minLeaseSeconds = 7200
	maxLeaseSeconds = 86400

	environmentMount = "/environment"
	workspaceMount   = "/workspace"
)

// Config is trusted operator configuration, never public Session input.
type Config struct {
	// InstallationID is the provider key: a stable UUID that owns every sandbox
	// this provider creates.
	InstallationID string
	// APIURL is the CubeAPI base, including the /cubeapi/v1 path prefix.
	APIURL string
	// ProxyNodeIP is the CubeProxy address used for the data plane. Outside a
	// node the sandbox domain does not resolve, so the client dials this address
	// while preserving the virtual Host header. It may be "host" or "host:port";
	// without a port the scheme default (80 for http, 443 for https) applies. It
	// may be empty only when the sandbox domain resolves from Core.
	ProxyNodeIP string
	// SandboxDomain is the vendor's sandbox domain suffix, for example cube.app.
	SandboxDomain string
	// ProxyScheme is http or https for the data plane.
	ProxyScheme string
	// Template is the operator-pinned template id.
	Template string
	// APIKey is the CubeAPI credential, read from a private file. It is held in
	// memory only and never logged, echoed, put in argv, labels or metadata.
	APIKey string
	// LeaseSeconds is the Cube idle TTL. It must exceed Core's expiry window.
	LeaseSeconds int
	// HostMountRoot is the operator-configured host directory prefix under which
	// every per-allocation store lives. CubeMaster must allow the same prefix in
	// allowed_host_mount_prefixes.
	HostMountRoot string
	// PlatformEgress lists the addresses the trusted Runtime path needs when a
	// Session disables network access. Core's own host is added automatically at
	// create time; the model endpoints the harness uses must be listed here. It is
	// never derived from Session input.
	PlatformEgress []string
}

type Provider struct {
	config  Config
	control *http.Client
	data    *http.Client
}

var _ sandbox.Provider = (*Provider)(nil)

// New validates operator configuration. Every rejection is sandbox.ErrInvalid:
// no half-configured provider ever reaches a cluster.
func New(config Config) (*Provider, error) {
	if !validID(config.InstallationID) || strings.TrimSpace(config.Template) == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.SandboxDomain) == "" {
		return nil, sandbox.ErrInvalid
	}
	if config.ProxyScheme != "http" && config.ProxyScheme != "https" {
		return nil, sandbox.ErrInvalid
	}
	if config.LeaseSeconds < minLeaseSeconds || config.LeaseSeconds > maxLeaseSeconds {
		return nil, sandbox.ErrInvalid
	}
	if !validHostMountRoot(config.HostMountRoot) || !validProxyNode(config.ProxyNodeIP) {
		return nil, sandbox.ErrInvalid
	}
	u, err := url.Parse(config.APIURL)
	if err != nil || !validAbsoluteURL(u) {
		return nil, sandbox.ErrInvalid
	}
	for _, host := range config.PlatformEgress {
		if !validEgressHost(host) {
			return nil, sandbox.ErrInvalid
		}
	}
	return &Provider{config: config, control: newControlClient(), data: newDataClient(config)}, nil
}

// Close releases idle connections. It never touches a remote sandbox:
// reclamation stays on the explicit Kill path.
func (p *Provider) Close() {
	p.control.CloseIdleConnections()
	p.data.CloseIdleConnections()
}

func validID(v string) bool {
	u, e := uuid.Parse(v)
	return e == nil && u != uuid.Nil && u.String() == v
}

func validReference(r sandbox.Reference) bool {
	return validID(r.TenantID) && validID(r.EnvironmentID) && validID(r.AllocationID)
}

func validAbsoluteURL(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

// validHostMountRoot requires an operator-chosen absolute prefix that can never
// be the host root and never carries a traversal component. Every per-allocation
// store is derived from identity below it.
func validHostMountRoot(root string) bool {
	if root == "" || !path.IsAbs(root) || path.Clean(root) != root || root == "/" || strings.ContainsAny(root, " \t\n") {
		return false
	}
	return len(strings.Trim(root, "/")) > 0
}

// validProxyNode accepts a bare host or host:port without a path, credentials or
// whitespace.
func validProxyNode(value string) bool {
	if value == "" {
		return true
	}
	if strings.ContainsAny(value, " \t\n/\\@") {
		return false
	}
	host, port, err := splitHostPort(value)
	if err != nil || host == "" || (net.ParseIP(host) == nil && strings.Trim(host, "abcdefghijklmnopqrstuvwxyz0123456789.-") != "") {
		return false
	}
	if port == "" {
		return true
	}
	number, err := strconv.Atoi(port)
	return err == nil && number > 0 && number <= 65535
}

// validEgressHost accepts the address forms CubeVS understands: a hostname, an
// IP address or a CIDR. It rejects anything that would smuggle a second entry.
func validEgressHost(host string) bool {
	if host == "" || strings.ContainsAny(host, " \t\n,;\"'\\") {
		return false
	}
	return !strings.Contains(host, "//")
}

// splitHostPort splits "host", "host:port" and "[v6]:port" without requiring a
// port.
func splitHostPort(value string) (string, string, error) {
	if !strings.Contains(value, ":") {
		return value, "", nil
	}
	return net.SplitHostPort(value)
}

// metadata is the ownership identity. CubeMaster stores it as sandbox labels, and
// GET /sandboxes?metadata= filters on it server-side.
func (p *Provider) metadata(r sandbox.Reference) map[string]string {
	return map[string]string{
		labelPrefix + "installation": p.config.InstallationID,
		labelPrefix + "tenant":       r.TenantID,
		labelPrefix + "environment":  r.EnvironmentID,
		labelPrefix + "allocation":   r.AllocationID,
	}
}

// owns requires every ownership key to match exactly. A partial or foreign match
// is never enough to adopt or delete a sandbox.
func (p *Provider) owns(metadata map[string]string, r sandbox.Reference) bool {
	for key, value := range p.metadata(r) {
		if metadata[key] != value {
			return false
		}
	}
	return true
}

// storePath derives one per-allocation host directory from identity alone. User
// input has no influence on it, and the same directory backs both views so the
// trusted atomic staging rename never crosses a mount point.
func (p *Provider) storePath(r sandbox.Reference) string {
	return path.Join(p.config.HostMountRoot, p.config.InstallationID, r.TenantID, r.EnvironmentID, r.AllocationID)
}

// hostMounts is the vendor's storage descriptor: a JSON-encoded array in the
// create metadata under host-mount, which CubeAPI forwards to CubeMaster as the
// host-mount annotation.
func (p *Provider) hostMounts(r sandbox.Reference) (string, error) {
	root := p.storePath(r)
	raw, err := json.Marshal([]map[string]any{
		{"hostPath": root, "mountPath": environmentMount, "readOnly": false},
		{"hostPath": path.Join(root, "workspace"), "mountPath": workspaceMount, "readOnly": false},
	})
	if err != nil {
		return "", sandbox.ErrInvalid
	}
	return string(raw), nil
}

// network maps the Session policy onto the pinned egress model. An enabled or
// unspecified policy leaves the platform default, which allows ordinary egress.
// A disabled policy denies ordinary public egress and keeps the trusted Core and
// platform addresses reachable, because the daemon's connection to Core and the
// harness's model calls run over the same interface (§8.6).
func (p *Provider) network(access, coreHost string) map[string]any {
	if access != "disabled" {
		return nil
	}
	allow := []string{}
	if coreHost != "" {
		allow = append(allow, coreHost)
	}
	allow = append(allow, p.config.PlatformEgress...)
	return map[string]any{"allowOut": allow}
}

// Create provisions one sandbox for one allocation. The caller persisted the
// reference first, so a lost response is resolved by GetInfo and never by a
// second create.
func (p *Provider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	info := sandbox.Info{Reference: b.Reference}
	core, err := url.Parse(b.CoreURL)
	if !validReference(b.Reference) || !validID(b.SessionID) || !validID(b.DeviceID) || err != nil || !validAbsoluteURL(core) || strings.TrimSpace(b.Credential) == "" ||
		(b.NetworkAccess != "" && b.NetworkAccess != "enabled" && b.NetworkAccess != "disabled") || len(b.AllowedDomains) != 0 {
		return info, sandbox.ErrInvalid
	}
	if existing, err := p.GetInfo(ctx, b.Reference); err == nil {
		return existing, sandbox.ErrExists
	} else if !errors.Is(err, sandbox.ErrNotFound) {
		return info, err
	}
	mounts, err := p.hostMounts(b.Reference)
	if err != nil {
		return info, err
	}
	metadata := p.metadata(b.Reference)
	metadata[hostMountKey] = mounts
	body := map[string]any{
		"templateID": p.config.Template,
		"timeout":    p.config.LeaseSeconds,
		"metadata":   metadata,
	}
	if access := p.network(b.NetworkAccess, core.Host); access != nil {
		// The vendor installs 0.0.0.0/0 into deny_out for this flag, which is
		// what makes the allowOut entries above the only reachable addresses.
		body["allow_internet_access"] = false
		body["network"] = access
	}
	created, err := p.create(ctx, body)
	info.ProviderID = created.SandboxID
	if err != nil {
		// The allocation exists in an unknown state. Return the reference so the
		// caller keeps ownership of cleanup; never erase uncertain owner state.
		return info, err
	}
	if err := p.bootstrap(ctx, created, b); err != nil {
		return info, fmt.Errorf("runtime bootstrap: %w", err)
	}
	return p.waitReady(ctx, b.Reference)
}

// GetInfo is polled every five seconds per allocation, so it stays cheap and
// side-effect free: one filtered list, one detail read and one readiness probe.
func (p *Provider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	info := sandbox.Info{Reference: r}
	if !validReference(r) {
		return info, sandbox.ErrInvalid
	}
	observed, err := p.inspect(ctx, r)
	if err != nil {
		return info, err
	}
	info.ProviderID = observed.SandboxID
	info.State = mappedState(observed.State)
	if info.State == "running" {
		ready, reason := p.ready(ctx, observed, r)
		info.BootstrapComplete = ready
		if !ready {
			obslog.Debug(ctx, "cubesandbox Runtime is not ready", "allocation_id", r.AllocationID, "environment_id", r.EnvironmentID, "sandbox_id", observed.SandboxID, "reason", reason)
		}
	}
	return info, nil
}

// Renew extends the TTL of the original sandbox only. It never creates, pauses,
// resumes, restarts or replaces anything, and it never reports a lease it did
// not observe.
func (p *Provider) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	info := sandbox.Info{Reference: r}
	if !validReference(r) {
		return info, sandbox.ErrInvalid
	}
	observed, err := p.inspect(ctx, r)
	if err != nil {
		return info, err
	}
	info.ProviderID = observed.SandboxID
	info.State = mappedState(observed.State)
	if info.State != "running" {
		return info, errors.New("CubeSandbox allocation is not running")
	}
	if err := p.refresh(ctx, observed.SandboxID); err != nil {
		return info, err
	}
	return p.GetInfo(ctx, r)
}

// Kill is idempotent for absence only. It verifies every owned match before
// deleting any, never prunes broadly, then confirms that nothing matches.
func (p *Provider) Kill(ctx context.Context, r sandbox.Reference) error {
	if !validReference(r) {
		return sandbox.ErrInvalid
	}
	matches, err := p.list(ctx, r)
	if err != nil {
		return err
	}
	// Every candidate is verified before the first delete. A candidate the vendor
	// cannot identify, or whose identity does not match, is ambiguous state and
	// stops the operation instead of becoming a path-derived delete target.
	verified := make([]string, 0, len(matches))
	for _, match := range matches {
		if match.SandboxID == "" {
			return sandbox.ErrOwnership
		}
		if _, err := p.inspectID(ctx, match.SandboxID, r); err != nil {
			if errors.Is(err, sandbox.ErrNotFound) {
				// Already gone: there is nothing left to delete for it.
				continue
			}
			return err
		}
		verified = append(verified, match.SandboxID)
	}
	for _, id := range verified {
		if err := p.delete(ctx, id); err != nil && !errors.Is(err, sandbox.ErrNotFound) {
			return err
		}
	}
	remaining, err := p.list(ctx, r)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return errors.New("CubeSandbox removal unconfirmed")
	}
	return nil
}

// mappedState reports only the pinned state vocabulary. Anything the vendor
// reports that is not one of those values is unknown, never running.
func mappedState(state string) string {
	switch state {
	case "running":
		return "running"
	case "paused":
		return "paused"
	case "pausing":
		return "pausing"
	default:
		return "unknown"
	}
}
