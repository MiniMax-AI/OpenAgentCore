// Package e2b adapts the official SDK helper to managed compute operations.
package e2b

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

const ProtocolVersion = 1
const SDKVersion = "2.51.0"
const MaxOutputBytes = 1024 * 1024
const MaxRequestBytes = 72 * 1024 * 1024
const MaxResponseBytes = 16 * 1024 * 1024

// Config contains trusted deployment configuration; APIKey travels only on stdin.
type Config struct {
	Binary, StateDir, InstallationID, APIKey, Template string
	TimeoutSeconds                                     int
	Resources                                          *sandbox.Resources
}

type Request struct {
	Version   int
	Operation string
	Config    Config
	Reference sandbox.Reference
	Bootstrap *sandbox.Bootstrap `json:",omitempty"`
	Command   *sandbox.Command   `json:",omitempty"`
	Deadline  time.Time
}
type Response struct {
	Version         int
	Info            *sandbox.Info          `json:",omitempty"`
	Command         *sandbox.CommandResult `json:",omitempty"`
	ErrorCode       string
	DeploymentValid bool `json:",omitempty"`
}
type Caller interface {
	Call(context.Context, Request) (Response, error)
}
type Provider struct {
	config Config
	caller Caller
}

var _ sandbox.Provider = (*Provider)(nil)

func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
func validReference(r sandbox.Reference) bool {
	return validID(r.TenantID) && validID(r.EnvironmentID) && validID(r.AllocationID)
}
func (c Config) Validate() error {
	if c.Resources != nil && c.Resources.Validate("e2b") != nil {
		return sandbox.ErrInvalid
	}
	template, build, ok := strings.Cut(c.Template, ":")
	if !ok || template == "" || !validID(build) || !validID(c.InstallationID) || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 86400 || c.APIKey == "" || len(c.APIKey) > 4096 || strings.ContainsFunc(c.APIKey, func(r rune) bool { return unicode.IsSpace(r) || r == 0 }) {
		return sandbox.ErrInvalid
	}
	for _, r := range template {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return sandbox.ErrInvalid
		}
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
	return &Provider{config: c, caller: caller}, nil
}
func (p *Provider) call(ctx context.Context, operation string, r sandbox.Reference, b *sandbox.Bootstrap, command *sandbox.Command) (Response, error) {
	deadline, ok := ctx.Deadline()
	if !ok || (operation != "validate_deployment" && !validReference(r)) {
		return unstarted(operation, r), sandbox.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return unstarted(operation, r), err
	}
	out, err := p.caller.Call(ctx, Request{Version: ProtocolVersion, Operation: operation, Config: p.config, Reference: r, Bootstrap: b, Command: command, Deadline: deadline})
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

// ValidateDeployment reads the exact immutable build without creating compute or
// allocation receipts. Candidate configuration remains unpublished until it passes.
func (p *Provider) ValidateDeployment(ctx context.Context) error {
	if p.config.Resources == nil {
		return sandbox.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := p.call(ctx, "validate_deployment", sandbox.Reference{}, nil, nil)
	if err != nil {
		return err
	}
	if !out.DeploymentValid || out.Info != nil || out.Command != nil {
		return sandbox.ErrComputeUnconfirmed
	}
	return nil
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
	u, err := url.Parse(b.CoreURL)
	policy := agentnetwork.Policy{Access: b.NetworkAccess, AllowedDomains: b.AllowedDomains}
	if !validReference(b.Reference) || !validID(b.SessionID) || !validID(b.DeviceID) || policy.Validate() != nil || err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(b.Credential) == "" {
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
