// Package e2b adapts the official SDK helper to managed compute operations.
package e2b

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentnetwork"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimebootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

const ProtocolVersion = 1
const SDKVersion = "2.51.0"
const MaxOutputBytes = 1024 * 1024
const MaxRequestBytes = 72 * 1024 * 1024
const MaxResponseBytes = 16 * 1024 * 1024

// Config contains trusted deployment configuration; APIKey travels only on stdin.
type Config struct {
	Binary, StateDir, InstallationID, APIKey, Template, APIURL, Domain string
	TimeoutSeconds                                                     int
	Resources                                                          *sandbox.Resources
}

type Request struct {
	Version   int
	Operation string
	Config    Config
	Reference sandbox.Reference
	// References lists the allocations of one read-only observe request.
	References       []sandbox.Reference          `json:",omitempty"`
	Bootstrap        *sandbox.Bootstrap           `json:",omitempty"`
	RuntimeBootstrap *runtimebootstrap.Connection `json:",omitempty"`
	Command          *sandbox.Command             `json:",omitempty"`
	Deadline         time.Time
}
type Response struct {
	Version         int
	Info            *sandbox.Info          `json:",omitempty"`
	Command         *sandbox.CommandResult `json:",omitempty"`
	ErrorCode       string
	DeploymentValid bool              `json:",omitempty"`
	TemplateBuild   *TemplateBuild    `json:",omitempty"`
	Templates       []TemplateSummary `json:",omitempty"`
	Builds          []ReadyBuild      `json:",omitempty"`
	Observations    []Observation     `json:",omitempty"`
}

type TemplateSummary struct {
	ID    string   `json:"id"`
	Names []string `json:"names"`
}
type ReadyBuild struct {
	ID        string `json:"id"`
	CPUs      uint32 `json:"cpus"`
	MemoryMiB uint32 `json:"memory_mib"`
}

// Discover uses the same pinned SDK helper without requiring a saved deployment.
// Its credential is passed only to the helper on stdin.
func Discover(ctx context.Context, caller Caller, binary, apiKey, apiURL, domain, template string) (Response, error) {
	if caller == nil || !filepath.IsAbs(binary) || apiKey == "" || len(apiKey) > 4096 ||
		strings.ContainsFunc(apiKey, func(r rune) bool { return unicode.IsSpace(r) || r == 0 }) {
		return Response{}, sandbox.ErrInvalid
	}
	if _, _, err := NormalizeEndpoint(apiURL, domain); err != nil {
		return Response{}, sandbox.ErrInvalid
	}
	operation := "list_templates"
	if template != "" {
		if len(template) > 128 {
			return Response{}, sandbox.ErrInvalid
		}
		for _, r := range template {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return Response{}, sandbox.ErrInvalid
			}
		}
		operation = "list_builds"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	out, err := caller.Call(ctx, Request{Version: ProtocolVersion, Operation: operation, Config: Config{Binary: binary, APIKey: apiKey, APIURL: apiURL, Domain: domain, Template: template}, Deadline: deadline})
	if err != nil || out.Version != ProtocolVersion {
		return Response{}, sandbox.ErrComputeUnconfirmed
	}
	if out.ErrorCode == "invalid" {
		return Response{}, sandbox.ErrInvalid
	}
	if out.ErrorCode != "" || out.Info != nil || out.Command != nil || out.TemplateBuild != nil || out.Observations != nil {
		return Response{}, sandbox.ErrComputeUnconfirmed
	}
	if operation == "list_templates" {
		if out.Templates == nil || out.Builds != nil || len(out.Templates) > 200 {
			return Response{}, sandbox.ErrComputeUnconfirmed
		}
		for _, item := range out.Templates {
			if item.ID == "" || len(item.ID) > 128 || len(item.Names) > 20 {
				return Response{}, sandbox.ErrComputeUnconfirmed
			}
			for _, r := range item.ID {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
					return Response{}, sandbox.ErrComputeUnconfirmed
				}
			}
			for _, name := range item.Names {
				if len(name) > 128 {
					return Response{}, sandbox.ErrComputeUnconfirmed
				}
			}
		}
	} else {
		if out.Builds == nil || out.Templates != nil || len(out.Builds) > 200 {
			return Response{}, sandbox.ErrComputeUnconfirmed
		}
		for _, item := range out.Builds {
			if !validID(item.ID) || item.CPUs == 0 || item.MemoryMiB == 0 {
				return Response{}, sandbox.ErrComputeUnconfirmed
			}
		}
	}
	return out, nil
}

// TemplateBuild is the fixed build as read by deployment validation.
// RootDiskMiB is nil when E2B does not report the build's disk size.
type TemplateBuild struct {
	Status          string
	CPUs, MemoryMiB uint32
	RootDiskMiB     *uint32
}
type Caller interface {
	Call(context.Context, Request) (Response, error)
}
type Provider struct {
	config Config
	caller Caller
	now    func() time.Time
}

var _ sandbox.SandboxProvider = (*Provider)(nil)

func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
func validReference(r sandbox.Reference) bool {
	return validID(r.TenantID) && validID(r.EnvironmentID) && validID(r.AllocationID)
}
func (c Config) Validate() error {
	if c.Resources != nil && ValidateResources(*c.Resources) != nil {
		return sandbox.ErrInvalid
	}
	if !validID(c.InstallationID) || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 86400 {
		return sandbox.ErrInvalid
	}
	if err := ValidateConfiguration(&DeploymentConfiguration{APIKey: c.APIKey, Template: c.Template, APIURL: c.APIURL, Domain: c.Domain}); err != nil {
		return err
	}
	for _, path := range []string{c.Binary, c.StateDir} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return sandbox.ErrInvalid
		}
	}
	binary, err := os.Stat(c.Binary)
	if err != nil || !binary.Mode().IsRegular() || binary.Mode().Perm()&0111 == 0 {
		return sandbox.ErrInvalid
	}
	state, err := os.Lstat(c.StateDir)
	if err != nil || !state.IsDir() || state.Mode().Perm()&0077 != 0 {
		return sandbox.ErrInvalid
	}
	return nil
}
func New(c Config) (*Provider, error) { return NewWithCaller(c, &ProcessCaller{}) }
func NewWithCaller(c Config, caller Caller) (*Provider, error) {
	if c.Validate() != nil || caller == nil {
		return nil, sandbox.ErrInvalid
	}
	if c.Resources != nil {
		resources := *c.Resources
		c.Resources = &resources
	}
	return &Provider{config: c, caller: caller, now: time.Now}, nil
}
func (p *Provider) call(ctx context.Context, operation string, r sandbox.Reference, b *sandbox.Bootstrap, command *sandbox.Command) (Response, error) {
	deadline, ok := ctx.Deadline()
	if !ok || (operation != "validate_deployment" && !validReference(r)) {
		return unstarted(operation, r), sandbox.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return unstarted(operation, r), err
	}
	var connection *runtimebootstrap.Connection
	if b != nil {
		value := b.RuntimeConnection()
		if value.Validate() != nil {
			return unstarted(operation, r), sandbox.ErrInvalid
		}
		connection = &value
	}
	out, err := p.caller.Call(ctx, Request{Version: ProtocolVersion, Operation: operation, Config: p.config, Reference: r, Bootstrap: b, RuntimeBootstrap: connection, Command: command, Deadline: deadline})
	if errors.Is(err, errHelperNotStarted) {
		return unstarted(operation, r), sandbox.ErrComputeUnconfirmed
	}
	if err != nil || out.Version != ProtocolVersion {
		if operation == "command" {
			return Response{}, sandbox.ErrCommandUnconfirmed
		}
		return Response{}, sandbox.ErrComputeUnconfirmed
	}
	if out.Info != nil && (out.Info.Reference != r || len(out.Info.ProviderID) > 256 || len(out.Info.State) > 64 || out.Info.BootstrapComplete && !out.Info.CreateSettled) {
		return Response{}, sandbox.ErrComputeUnconfirmed
	}
	switch out.ErrorCode {
	case "":
		return out, nil
	case "template_invalid":
		return out, fmt.Errorf("%w: This E2B template lacks the current Runtime startup entry point. Build a template with this release's build-template.py and select it in the sandbox deployment.", sandbox.ErrInvalid)
	case "team_mismatch":
		return out, sandbox.ErrCredentialOwnership
	case "unauthorized":
		return out, sandbox.ErrCredentialRejected
	case "invalid":
		return out, sandbox.ErrInvalid
	case "ownership":
		return out, sandbox.ErrOwnership
	case "exists":
		return out, sandbox.ErrExists
	case "not_found":
		return out, sandbox.ErrNotFound
	case "command_unconfirmed":
		return out, sandbox.ErrCommandUnconfirmed
	default:
		return out, sandbox.ErrComputeUnconfirmed
	}
}

// ValidateDeployment verifies team ownership and reads the exact immutable build without creating compute or
// allocation receipts. Candidate configuration remains unpublished until it passes.
// It returns the build as read. Without configured Resources it only requires a
// ready build, whose CPU and memory the caller then adopts as the selection.
func (p *Provider) ValidateDeployment(ctx context.Context) (TemplateBuild, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := p.call(ctx, "validate_deployment", sandbox.Reference{}, nil, nil)
	if err != nil {
		return TemplateBuild{}, err
	}
	build := out.TemplateBuild
	if !out.DeploymentValid || out.Info != nil || out.Command != nil || out.Observations != nil || build == nil || build.Status != "ready" ||
		build.CPUs == 0 || build.MemoryMiB == 0 || build.RootDiskMiB != nil && *build.RootDiskMiB == 0 ||
		p.config.Resources != nil && (build.CPUs != p.config.Resources.CPUs || build.MemoryMiB != p.config.Resources.MemoryMiB) {
		return TemplateBuild{}, sandbox.ErrComputeUnconfirmed
	}
	return *build, nil
}
func (p *Provider) info(ctx context.Context, operation string, r sandbox.Reference, b *sandbox.Bootstrap) (sandbox.Info, error) {
	out, err := p.call(ctx, operation, r, b, nil)
	if out.Info != nil {
		return *out.Info, err
	}
	if err == nil {
		err = sandbox.ErrComputeUnconfirmed
	}
	return sandbox.Info{Reference: r}, err
}
func (p *Provider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	policy := agentnetwork.Policy{Access: b.NetworkAccess, AllowedDomains: b.AllowedDomains}
	if !validReference(b.Reference) || !validID(b.SessionID) || !validID(b.DeviceID) || policy.Validate() != nil || b.RuntimeConnection().Validate() != nil {
		info := sandbox.Info{Reference: b.Reference}
		if validReference(b.Reference) {
			info.State, info.CreateSettled = "absent", true
		}
		return info, sandbox.ErrInvalid
	}
	b.AllowedDomains = policy.Hosts()
	return p.info(ctx, "create", b.Reference, &b)
}
func (p *Provider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.info(ctx, "inspect", r, nil)
}
func (p *Provider) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.info(ctx, "renew", r, nil)
}
func (p *Provider) Kill(ctx context.Context, r sandbox.Reference) error {
	out, err := p.call(ctx, "kill", r, nil, nil)
	if err == nil && (out.Info == nil || !out.Info.CreateSettled || out.Info.State != "absent") {
		return sandbox.ErrComputeUnconfirmed
	}
	return err
}
func (p *Provider) RunCommand(ctx context.Context, r sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	if len(c.Args) == 0 || len(c.Stdin) > sandbox.MaxCommandInputBytes || c.Directory != "" && !filepath.IsAbs(c.Directory) {
		return sandbox.CommandResult{}, sandbox.ErrInvalid
	}
	for _, arg := range c.Args {
		if strings.ContainsRune(arg, 0) {
			return sandbox.CommandResult{}, sandbox.ErrInvalid
		}
	}
	out, err := p.call(ctx, "command", r, nil, &c)
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	if out.Command == nil || len(out.Command.Stdout) > MaxOutputBytes || len(out.Command.Stderr) > MaxOutputBytes {
		return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
	}
	return *out.Command, nil
}

// A fresh allocation Create rejected before process startup has no cloud effects.
// The common Provider contract forbids replaying an earlier unknown Create.
func unstarted(operation string, r sandbox.Reference) Response {
	if operation == "create" && validReference(r) {
		return Response{Info: &sandbox.Info{Reference: r, State: "absent", CreateSettled: true}}
	}
	return Response{}
}
