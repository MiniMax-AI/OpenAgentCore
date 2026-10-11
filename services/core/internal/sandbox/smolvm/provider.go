// Package smolvm adapts the local smolvm service to OpenAgentCore's managed
// sandbox lifecycle. A VM and a durable bootstrap receipt are one allocation.
package smolvm

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentnetwork"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
	sdk "github.com/smol-machines/smolvm-sdk/smolvm-go"
)

const labelPrefix = "io.oac."

// Config is node-private; it does not come from a Session or an Environment.
// ImagePath is the verified local OCI archive from the selected distribution.
type Config struct {
	InstallationID, ImagePath, ImageHash, ReceiptRoot string
	Resources                                         sandbox.Resources
}

type Provider struct {
	client   *sdk.Client
	config   Config
	imageRef string
}

var _ sandbox.SandboxProvider = (*Provider)(nil)

func validID(v string) bool {
	id, err := uuid.Parse(v)
	return err == nil && id != uuid.Nil && id.String() == v
}
func validReference(r sandbox.Reference) bool {
	return validID(r.TenantID) && validID(r.EnvironmentID) && validID(r.AllocationID)
}
func New(client *sdk.Client, config Config) (*Provider, error) {
	if client == nil || !validID(config.InstallationID) || len(config.ImageHash) != 64 ||
		!filepath.IsAbs(config.ImagePath) || filepath.Clean(config.ImagePath) != config.ImagePath ||
		!filepath.IsAbs(config.ReceiptRoot) || filepath.Clean(config.ReceiptRoot) != config.ReceiptRoot ||
		ValidateResources(config.Resources) != nil {
		return nil, sandbox.ErrInvalid
	}
	if _, err := hex.DecodeString(config.ImageHash); err != nil || strings.ToLower(config.ImageHash) != config.ImageHash {
		return nil, sandbox.ErrInvalid
	}
	if err := os.MkdirAll(config.ReceiptRoot, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(config.ReceiptRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, sandbox.ErrInvalid
	}
	return &Provider{client: client, config: config, imageRef: "local:" + config.ImageHash}, nil
}
func (p *Provider) name(r sandbox.Reference) string {
	return resourceName(p.config.InstallationID, r)
}
func (p *Provider) labels(r sandbox.Reference, nativeID string) map[string]string {
	return map[string]string{
		labelPrefix + "installation": p.config.InstallationID,
		labelPrefix + "tenant":       r.TenantID,
		labelPrefix + "environment":  r.EnvironmentID,
		labelPrefix + "allocation":   r.AllocationID,
		labelPrefix + "native_id":    nativeID,
	}
}
func (p *Provider) owns(machine *sdk.MachineInfo, r sandbox.Reference) bool {
	for key, want := range p.labels(r, "") {
		if key == labelPrefix+"native_id" {
			if !validID(machine.Labels[key]) {
				return false
			}
			continue
		}
		if machine.Labels[key] != want {
			return false
		}
	}
	return machine.Name == p.name(r)
}
func (p *Provider) inspect(ctx context.Context, r sandbox.Reference) (*sdk.MachineInfo, error) {
	if !validReference(r) {
		return nil, sandbox.ErrInvalid
	}
	machine, err := p.client.GetMachine(ctx, p.name(r))
	if errors.Is(err, sdk.ErrNotFound) {
		return nil, sandbox.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !p.owns(machine, r) {
		return nil, sandbox.ErrOwnership
	}
	return machine, nil
}
func (p *Provider) describe(ctx context.Context, r sandbox.Reference, checkConfiguration bool) (sandbox.Info, error) {
	info := sandbox.Info{Reference: r}
	machine, err := p.inspect(ctx, r)
	if err != nil {
		return info, err
	}
	info.ProviderID, info.State = machine.Labels[labelPrefix+"native_id"], string(machine.State)
	receipt, err := p.readReceipt(r)
	if err != nil {
		return sandbox.Info{Reference: r}, err
	}
	if receipt != nil && receipt.NativeID != info.ProviderID {
		return sandbox.Info{Reference: r}, sandbox.ErrOwnership
	}
	if checkConfiguration {
		if machine.Image != p.imageRef || machine.CPUs != int(p.config.Resources.CPUs) || machine.MemoryMB != int(p.config.Resources.MemoryMiB) {
			return sandbox.Info{Reference: r}, sandbox.ErrInvalid
		}
	}
	info.BootstrapComplete = machine.State == sdk.MachineStateRunning && receipt != nil
	return info, nil
}
func (p *Provider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.describe(ctx, r, true)
}
func (p *Provider) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.GetInfo(ctx, r)
}

// Create installs exactly one immutable allocation, with a receipt written
// after guest bootstrap is ready. An uncertain error retains the original VM.
func (p *Provider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	info := sandbox.Info{Reference: b.Reference}
	if b.Workspace != nil {
		return info, &providercontract.UnsupportedError{Operation: "Create", Reason: "external_workspace_unsupported"}
	}
	policy := agentnetwork.Policy{Access: b.NetworkAccess, AllowedDomains: b.AllowedDomains}
	if !validReference(b.Reference) || !validID(b.SessionID) || !validID(b.DeviceID) || policy.Validate() != nil || b.RuntimeConnection().Validate() != nil {
		return info, sandbox.ErrInvalid
	}
	if previous, err := p.readReceipt(b.Reference); err != nil {
		return info, err
	} else if previous != nil {
		return info, sandbox.ErrExists
	}
	if _, err := p.inspect(ctx, b.Reference); err == nil {
		return info, sandbox.ErrExists
	} else if !errors.Is(err, sandbox.ErrNotFound) {
		return info, err
	}
	nativeID := uuid.NewString()
	machineName := p.name(b.Reference)
	domains, err := json.Marshal(policy.Hosts())
	if err != nil {
		return info, sandbox.ErrInvalid
	}
	// The workload waits for the final, atomic ready file before it connects.
	// This keeps an interrupted upload from starting a daemon with partial keys.
	command := `while [ ! -f /home/runtime/.oac/ready ]; do sleep 1; done; exec /usr/local/bin/oac-daemon connect --profile default --bootstrap-file /home/runtime/runtime-bootstrap.json`
	storage := diskGiB(p.config.Resources.RootDiskMiB)
	overlay := diskGiB(p.config.Resources.EnvironmentDiskMiB)
	created, err := p.client.CreateMachine(ctx, sdk.CreateMachineRequest{
		Name: machineName, Labels: p.labels(b.Reference, nativeID), Image: p.config.ImagePath,
		CPUs: int(p.config.Resources.CPUs), MemoryMB: int(p.config.Resources.MemoryMiB), StorageGB: &storage, OverlayGB: &overlay,
		Network: true, Entrypoint: []string{"/bin/sh", "-c"}, Cmd: []string{command},
		Env: []sdk.EnvVar{
			{Name: "OAC_RUNTIME_ENVIRONMENT_ID", Value: b.EnvironmentID},
			{Name: "OAC_RUNTIME_SESSION_ID", Value: b.SessionID},
			{Name: "OAC_RUNTIME_NETWORK_ACCESS", Value: policy.Access},
			{Name: "OAC_RUNTIME_ALLOWED_DOMAINS", Value: string(domains)},
		},
	})
	if errors.Is(err, sdk.ErrConflict) {
		return info, sandbox.ErrExists
	}
	if err != nil {
		return info, err
	}
	info.ProviderID = nativeID
	if !p.owns(created, b.Reference) {
		return info, sandbox.ErrOwnership
	}
	if created.Image != p.imageRef || created.CPUs != int(p.config.Resources.CPUs) || created.MemoryMB != int(p.config.Resources.MemoryMiB) {
		info.CreateSettled = true // create returned and no bootstrap mutation was dispatched
		return info, sandbox.ErrInvalid
	}
	if _, err := p.client.StartMachine(ctx, machineName); err != nil {
		return info, err
	}
	if err := p.bootstrap(ctx, b); err != nil {
		return info, fmt.Errorf("runtime bootstrap: %w", err)
	}
	if err := p.writeReceipt(b.Reference, nativeID); err != nil {
		return info, err
	}
	return p.GetInfo(ctx, b.Reference)
}

func diskGiB(mib uint32) int64 { return int64((uint64(mib) + 1023) / 1024) }

// Kill verifies the native VM and the private receipt before deleting either.
func (p *Provider) Kill(ctx context.Context, ref sandbox.Reference) error {
	if !validReference(ref) {
		return sandbox.ErrInvalid
	}
	machine, err := p.inspect(ctx, ref)
	if err != nil && !errors.Is(err, sandbox.ErrNotFound) {
		return err
	}
	receipt, receiptErr := p.readReceipt(ref)
	if receiptErr != nil {
		return receiptErr
	}
	if receipt != nil && machine != nil && receipt.NativeID != machine.Labels[labelPrefix+"native_id"] {
		return sandbox.ErrOwnership
	}
	if machine != nil {
		if _, err := p.client.DeleteMachine(ctx, machine.Name, false); err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return err
		}
		if _, err := p.inspect(ctx, ref); !errors.Is(err, sandbox.ErrNotFound) {
			if err == nil {
				return errors.New("smolvm VM deletion is unconfirmed")
			}
			return err
		}
	}
	if err := p.removeReceipt(ref); err != nil {
		return err
	}
	return nil
}

func (p *Provider) bootstrap(ctx context.Context, b sandbox.Bootstrap) error {
	name := p.name(b.Reference)
	noStart := false
	prepare, err := p.client.Exec(ctx, name, sdk.ExecRequest{Command: []string{"/bin/sh", "-c", `umask 077; mkdir -p /home/runtime/.oac /environment/workspace /environment/staging /environment/initialization /environment/packages; chown -R 1000:1000 /home/runtime /environment; chmod 0700 /home/runtime /home/runtime/.oac`}, User: "0", AutoStart: &noStart})
	if err != nil {
		return err
	}
	if prepare.ExitCode != 0 {
		return fmt.Errorf("prepare runtime directories exited %d", prepare.ExitCode)
	}
	credentials, err := b.RuntimeConnection().Marshal()
	if err != nil {
		return err
	}
	if _, err := p.client.UploadFile(ctx, name, "/home/runtime/runtime-bootstrap.json", credentials); err != nil {
		return err
	}
	permissions, err := p.client.Exec(ctx, name, sdk.ExecRequest{Command: []string{"/bin/sh", "-c", "chown 1000:1000 /home/runtime/runtime-bootstrap.json && chmod 0600 /home/runtime/runtime-bootstrap.json"}, User: "0", AutoStart: &noStart})
	if err != nil {
		return err
	}
	if permissions.ExitCode != 0 {
		return fmt.Errorf("set bootstrap ownership exited %d", permissions.ExitCode)
	}
	// The ready file contains no credentials. It is the final guest mutation:
	// the waiting workload starts the daemon without another provider RPC.
	_, err = p.client.UploadFile(ctx, name, "/home/runtime/.oac/ready", []byte("ready\n"))
	return err
}

func validateCommand(command sandbox.Command) error {
	if len(command.Args) == 0 || len(command.Stdin) > sandbox.MaxCommandInputBytes || command.Directory != "" && (!path.IsAbs(command.Directory) || path.Clean(command.Directory) != command.Directory) {
		return sandbox.ErrInvalid
	}
	for _, arg := range command.Args {
		if strings.ContainsRune(arg, 0) {
			return sandbox.ErrInvalid
		}
	}
	return nil
}
