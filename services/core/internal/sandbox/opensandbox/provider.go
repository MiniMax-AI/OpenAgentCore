package opensandbox

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	osdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

const (
	// Ownership metadata on every sandbox; the bootstrap marker is patched on
	// last, after the Runtime daemon started.
	keyInstallation = "io.oac.installation"
	keyTenant       = "io.oac.tenant"
	keyEnvironment  = "io.oac.environment"
	keyAllocation   = "io.oac.allocation"
	keyBootstrap    = "io.oac.bootstrap"

	// lease is the native expiry set at Create and extended by Renew.
	lease = time.Hour
	// Listing reads at most maxListPages pages of listPageSize sandboxes.
	listPageSize  = 100
	maxListPages  = 10
	maxOutput     = 1 << 20
	bootstrapFile = "/home/runtime/runtime-bootstrap.json"
	stdinDir      = "/home/runtime/.oac"
	// maxClockLead tolerates a metrics timestamp slightly ahead of Core's clock.
	maxClockLead = 30 * time.Second
)

// Provider runs sandboxes on one OpenSandbox server. A sandbox is identified by
// its ownership metadata alone.
type Provider struct {
	installation string
	image        string
	resources    sandbox.Resources
	connection   osdk.ConnectionConfig
	lifecycle    *osdk.LifecycleClient
	resolve      resolver
	now          func() time.Time
}

var _ sandbox.SandboxProvider = (*Provider)(nil)

// BuildDirect builds the Provider for one direct-mode selection.
func BuildDirect(c sandbox.DirectConfig) (sandbox.SandboxProvider, error) { return newProvider(c) }

func newProvider(c sandbox.DirectConfig) (*Provider, error) {
	deployment, err := validate(configuration(c.Selection))
	if err != nil || !validID(c.InstallationID) || c.Selection.Workspace != nil || c.Selection.DeploymentSpec.ValidatePolicy("opensandbox", Policy()) != nil {
		return nil, errors.New("OpenSandbox deployment configuration is unavailable")
	}
	scheme, _, _ := strings.Cut(deployment.APIURL, "://")
	// Every call reaches the same server, so one transport keeps its connections
	// alive; without it the SDK builds a new transport per connected sandbox.
	client := &http.Client{Transport: osdk.DefaultTransport()}
	return &Provider{
		installation: c.InstallationID, image: deployment.Image, resources: c.Selection.Resources,
		connection: osdk.ConnectionConfig{Domain: deployment.APIURL, Protocol: scheme, APIKey: deployment.APIKey, UseServerProxy: true, HTTPClient: client},
		lifecycle:  osdk.NewLifecycleClient(deployment.APIURL+"/"+osdk.APIVersion, deployment.APIKey, osdk.WithHTTPClient(client)),
		resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		now: time.Now,
	}, nil
}

func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validReference(r sandbox.Reference) bool {
	return validID(r.TenantID) && validID(r.EnvironmentID) && validID(r.AllocationID)
}

func (p *Provider) ownerMetadata(r sandbox.Reference) map[string]string {
	return map[string]string{keyInstallation: p.installation, keyTenant: r.TenantID, keyEnvironment: r.EnvironmentID, keyAllocation: r.AllocationID}
}

// nativeErr maps a failed native call. It never exposes SDK text.
func nativeErr(err error) error {
	if statusOf(err) == http.StatusUnauthorized || statusOf(err) == http.StatusForbidden {
		return sandbox.ErrCredentialRejected
	}
	return sandbox.ErrComputeUnconfirmed
}

// notSent reports a failure before the request reached the server: the dial,
// the name lookup or the server certificate failed.
func notSent(err error) bool {
	var lookup *net.DNSError
	var dial *net.OpError
	var certificate *tls.CertificateVerificationError
	return errors.As(err, &lookup) || errors.As(err, &dial) && dial.Op == "dial" || errors.As(err, &certificate)
}

// statusOf is the HTTP status of a native error response, or 0.
func statusOf(err error) int {
	var api *osdk.APIError
	if errors.As(err, &api) {
		return api.StatusCode
	}
	return 0
}

// owned lists the sandboxes carrying the allocation's four ownership keys and
// checks each item's values exactly.
func (p *Provider) owned(ctx context.Context, r sandbox.Reference) ([]osdk.SandboxInfo, error) {
	want := p.ownerMetadata(r)
	var items []osdk.SandboxInfo
	for page := 1; page <= maxListPages; page++ {
		list, err := p.lifecycle.ListSandboxes(ctx, osdk.ListOptions{Metadata: want, Page: page, PageSize: listPageSize})
		if err != nil {
			return nil, nativeErr(err)
		}
		for _, item := range list.Items {
			if !owns(item, want) {
				return nil, sandbox.ErrOwnership
			}
			items = append(items, item)
		}
		if !list.Pagination.HasNextPage {
			return items, nil
		}
	}
	return nil, sandbox.ErrComputeUnconfirmed
}

func owns(item osdk.SandboxInfo, want map[string]string) bool {
	for key, value := range want {
		if got, ok := item.Metadata[key]; !ok || got != value {
			return false
		}
	}
	return true
}

// info reports one owned sandbox. One create request yields at most one
// sandbox, every later effect of the attempt is inside it, and Kill removes it,
// so an observed sandbox in any state proves the attempt can no longer leave
// resources behind; Core records it before Kill.
func info(r sandbox.Reference, item osdk.SandboxInfo) sandbox.Info {
	return sandbox.Info{
		Reference: r, ProviderID: item.ID, State: strings.ToLower(string(item.Status.State)),
		BootstrapComplete: item.Metadata[keyBootstrap] == "complete", CreateSettled: true,
	}
}

// lookup finds the allocation's one sandbox. An image other than the configured
// one returns the Info with ErrInvalid, so cleanup still proceeds.
func (p *Provider) lookup(ctx context.Context, r sandbox.Reference) (sandbox.Info, osdk.SandboxInfo, error) {
	items, err := p.owned(ctx, r)
	if err == nil {
		switch len(items) {
		case 0:
			err = sandbox.ErrNotFound
		case 1:
			if item := items[0]; item.Image != nil && item.Image.URI != p.image {
				return info(r, item), item, sandbox.ErrInvalid
			}
			return info(r, items[0]), items[0], nil
		default:
			err = sandbox.ErrComputeUnconfirmed
		}
	}
	return sandbox.Info{Reference: r}, osdk.SandboxInfo{}, err
}

func (p *Provider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	if !validReference(r) {
		return sandbox.Info{}, sandbox.ErrInvalid
	}
	got, _, err := p.lookup(ctx, r)
	return got, err
}

// Renew extends the native lease of a running sandbox. Any other state is
// reported, never renewed.
func (p *Provider) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	if !validReference(r) {
		return sandbox.Info{}, sandbox.ErrInvalid
	}
	got, item, err := p.lookup(ctx, r)
	if err != nil {
		return got, err
	}
	if item.Status.State != osdk.StateRunning {
		return got, sandbox.ErrComputeUnconfirmed
	}
	if _, err := p.lifecycle.RenewExpiration(ctx, item.ID, p.now().Add(lease)); err != nil {
		return sandbox.Info{Reference: r}, nativeErr(err)
	}
	got, _, err = p.lookup(ctx, r)
	return got, err
}

// Kill deletes every sandbox of the allocation after verifying that all of them
// are owned, then confirms none is left.
func (p *Provider) Kill(ctx context.Context, r sandbox.Reference) error {
	if !validReference(r) {
		return sandbox.ErrInvalid
	}
	items, err := p.owned(ctx, r)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := p.lifecycle.DeleteSandbox(ctx, item.ID); err != nil && statusOf(err) != http.StatusNotFound {
			return nativeErr(err)
		}
	}
	if items, err = p.owned(ctx, r); err != nil {
		return err
	}
	if len(items) > 0 {
		return sandbox.ErrComputeUnconfirmed
	}
	return nil
}

func (p *Provider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	r := b.Reference
	// Nothing was dispatched before the native create: the allocation is absent.
	rejected := func(err error) (sandbox.Info, error) {
		if !validReference(r) {
			return sandbox.Info{Reference: r}, err
		}
		return sandbox.Info{Reference: r, State: "absent", CreateSettled: true}, err
	}
	if b.Workspace != nil {
		return rejected(sandbox.ErrInvalid)
	}
	connection := b.RuntimeConnection()
	if !validReference(r) || !validID(b.DeviceID) || connection.Validate() != nil || b.Egress.Validate() != nil || len(b.Egress.PrivateDomains) > 0 {
		return rejected(sandbox.ErrInvalid)
	}
	policy, err := networkPolicy(ctx, b, p.resolve)
	if err != nil {
		return rejected(err)
	}
	existing, err := p.owned(ctx, r)
	switch {
	case errors.Is(err, sandbox.ErrOwnership):
		return sandbox.Info{Reference: r}, err
	case err != nil:
		return rejected(err)
	case len(existing) > 0:
		return sandbox.Info{Reference: r}, sandbox.ErrExists
	}

	data, err := connection.Marshal()
	if err != nil {
		return rejected(sandbox.ErrInvalid)
	}
	timeout := int(lease / time.Second)
	created, err := p.lifecycle.CreateSandbox(ctx, osdk.CreateSandboxRequest{
		Image: &osdk.ImageSpec{URI: p.image}, Entrypoint: []string{"sleep", "infinity"}, Timeout: &timeout,
		ResourceLimits: osdk.ResourceLimits{"cpu": strconv.Itoa(int(p.resources.CPUs)), "memory": strconv.Itoa(int(p.resources.MemoryMiB)) + "Mi"},
		Metadata:       p.ownerMetadata(r), NetworkPolicy: policy,
	})
	if err != nil {
		return p.failedCreate(ctx, r, err, rejected)
	}
	// The create returned its one sandbox, so the attempt is settled from here on.
	got := info(r, *created)
	item, err := p.lifecycle.GetSandbox(ctx, created.ID)
	if err != nil {
		return got, nativeErr(err)
	}
	got = info(r, *item)
	if item.ID != created.ID || !owns(*item, p.ownerMetadata(r)) {
		return got, sandbox.ErrOwnership
	}
	// The egress sidecar starts answering after the create returns, so its
	// policy status is part of the SDK's readiness wait.
	var mode string
	live, err := osdk.ConnectSandbox(ctx, p.connection, item.ID, osdk.ReadyOptions{HealthCheck: func(ctx context.Context, s *osdk.Sandbox) (bool, error) {
		if err := s.Ping(ctx); err != nil {
			return false, err
		}
		status, err := p.lifecycle.GetNetworkPolicy(ctx, item.ID)
		if err != nil {
			return false, err
		}
		mode = status.EnforcementMode
		return true, nil
	}})
	if err != nil {
		return got, nativeErr(err)
	}
	if mode != "dns+nft" {
		return got, fmt.Errorf("%w: the OpenSandbox egress sidecar must run in dns+nft mode", sandbox.ErrInvalid)
	}
	if err := upload(ctx, live, bootstrapFile, data); err != nil {
		return got, nativeErr(err)
	}
	result, err := run(ctx, live, []string{"/usr/local/bin/oac-daemon", "connect", "--profile", "default", "--bootstrap-file", bootstrapFile, "-b"}, "/environment/workspace")
	if err != nil {
		return got, err
	}
	if result.ExitCode != 0 {
		return got, sandbox.ErrCommandUnconfirmed
	}
	marker := "complete"
	if _, err := p.lifecycle.PatchSandboxMetadata(ctx, item.ID, osdk.MetadataPatch{keyBootstrap: &marker}); err != nil {
		return got, nativeErr(err)
	}
	got, _, err = p.lookup(ctx, r)
	if err == nil && !got.BootstrapComplete {
		err = sandbox.ErrCommandUnconfirmed
	}
	return got, err
}

// failedCreate classifies a create request that returned no sandbox. A request
// that never reached the server created nothing, and the server checks the key
// before it handles a request. Only the server's handlers answer with a
// namespaced code such as DOCKER::SANDBOX_IMAGE_PULL_FAILED, after their
// rollback, so a listing then shows whether anything outlived it. Any other
// failure, a gateway's answer included, may hide a sandbox that appears later.
func (p *Provider) failedCreate(ctx context.Context, r sandbox.Reference, err error, rejected func(error) (sandbox.Info, error)) (sandbox.Info, error) {
	if notSent(err) {
		return rejected(sandbox.ErrComputeUnconfirmed)
	}
	var native *osdk.APIError
	switch {
	case !errors.As(err, &native):
		return sandbox.Info{Reference: r}, sandbox.ErrComputeUnconfirmed
	case native.StatusCode == http.StatusUnauthorized:
		return rejected(sandbox.ErrCredentialRejected)
	case !strings.Contains(native.Response.Code, "::"):
		return sandbox.Info{Reference: r}, sandbox.ErrComputeUnconfirmed
	}
	outcome := sandbox.ErrComputeUnconfirmed
	if native.StatusCode >= 400 && native.StatusCode < 500 {
		outcome = sandbox.ErrInvalid
	}
	switch items, err := p.owned(ctx, r); {
	case err != nil:
		return sandbox.Info{Reference: r}, sandbox.ErrComputeUnconfirmed
	case len(items) > 0:
		return info(r, items[0]), outcome
	}
	return rejected(outcome)
}

// upload writes a private file through execd.
func upload(ctx context.Context, live *osdk.Sandbox, path string, data []byte) error {
	return live.UploadFile(ctx, bytes.NewReader(data), osdk.UploadFileOptions{FileName: filepath.Base(path), Metadata: osdk.FileMetadata{Path: path, Mode: osdk.OctalMode(0o600)}})
}

var errOutputLimit = errors.New("command output exceeds the limit")

// run executes argv and collects its output and real exit code. A stream that
// ends without an exit code, or output over the limit, is unconfirmed.
func run(ctx context.Context, live *osdk.Sandbox, argv []string, directory string) (sandbox.CommandResult, error) {
	var stdout, stderr strings.Builder
	collect := func(out *strings.Builder) func(osdk.OutputMessage) error {
		return func(m osdk.OutputMessage) error {
			if out.Len()+len(m.Text) > maxOutput {
				return errOutputLimit
			}
			out.WriteString(m.Text)
			return nil
		}
	}
	request := osdk.RunCommandRequest{Argv: argv, Cwd: directory}
	if deadline, ok := ctx.Deadline(); ok {
		request.Timeout = max(time.Until(deadline).Milliseconds(), 1)
	}
	execution, err := live.RunCommandWithOpts(ctx, request, &osdk.ExecutionHandlers{SkipAccumulation: true, OnStdout: collect(&stdout), OnStderr: collect(&stderr)})
	if err != nil || execution.ExitCode == nil {
		return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
	}
	return sandbox.CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: *execution.ExitCode}, nil
}

func validCommand(c sandbox.Command) error {
	if len(c.Args) == 0 || len(c.Stdin) > sandbox.MaxCommandInputBytes || c.Directory != "" && !filepath.IsAbs(c.Directory) {
		return sandbox.ErrInvalid
	}
	for _, arg := range c.Args {
		if strings.ContainsRune(arg, 0) {
			return sandbox.ErrInvalid
		}
	}
	return nil
}

// RunCommand runs once and never replays. Stdin is staged in a private file so
// that confidential bytes never enter argv.
func (p *Provider) RunCommand(ctx context.Context, r sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	if err := validCommand(c); err != nil || !validReference(r) {
		return sandbox.CommandResult{}, sandbox.ErrInvalid
	}
	got, item, err := p.lookup(ctx, r)
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	if got.State != "running" || !got.BootstrapComplete {
		return sandbox.CommandResult{}, sandbox.ErrInvalid
	}
	live, err := osdk.ConnectSandbox(ctx, p.connection, item.ID)
	if err != nil {
		return sandbox.CommandResult{}, nativeErr(err)
	}
	argv := c.Args
	if len(c.Stdin) > 0 {
		file := stdinDir + "/opensandbox-stdin-" + uuid.NewString()
		if err := upload(ctx, live, file, c.Stdin); err != nil {
			return sandbox.CommandResult{}, nativeErr(err)
		}
		argv = append([]string{"/bin/sh", "-c", `f=$1; shift; "$@" <"$f"; s=$?; rm -f "$f"; exit $s`, "sh", file}, c.Args...)
	}
	return run(ctx, live, argv, c.Directory)
}
