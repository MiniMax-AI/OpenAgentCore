//go:build linux

package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	wire "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

// The existing auth profile and daemon's own background mode are reused.
// All credential bytes enter the guest on stdin before any native work is admitted.
const bootstrapScript = `
import ctypes,json,os,stat,subprocess,sys
b=json.load(sys.stdin)
for p in ['/home/runtime','/home/runtime/.oac','/home/runtime/.oac/daemon','/home/runtime/.oac/daemon/default','/environment','/environment/workspace','/environment/staging','/environment/initialization','/environment/packages','/run/oac']:
    os.makedirs(p,mode=0o700,exist_ok=True)
    if not stat.S_ISDIR(os.lstat(p).st_mode): raise RuntimeError('invalid bootstrap directory')
    os.chmod(p,0o700);os.chown(p,1000,1000)
p='/home/runtime/.oac/daemon/default/auth.json'
fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
with os.fdopen(fd,'w') as f:
    json.dump({'server_url':b['CoreURL'],'runtime_id':b['DeviceID'],'runner_credential':b['Credential']},f)
    f.flush();os.fsync(f.fileno());os.fchown(f.fileno(),1000,1000)
if not stat.S_ISDIR(os.lstat('/workspace').st_mode): raise RuntimeError('invalid workspace alias')
libc=ctypes.CDLL(None,use_errno=True)
if libc.mount(b'/environment/workspace',b'/workspace',None,4096,None)!=0:
    raise OSError(ctypes.get_errno(),'workspace bind mount failed')
def runtime_user():
    os.setgroups([]);os.setgid(1000);os.setuid(1000)
subprocess.run(['/usr/local/bin/oac-daemon','connect','--profile','default','-b'],
               stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,
               cwd='/environment/workspace',preexec_fn=runtime_user,check=True)
`

func (b backend) create(ctx context.Context) (wire.Response, error) {
	c := wire.Compute{Name: wire.Name(b.q.Config, b.q.Reference, 0)}
	if _, _, e := b.inspect(ctx, c); e == nil {
		return wire.Response{}, sandbox.ErrExists
	} else if !sdk.IsKind(e, sdk.ErrSandboxNotFound) {
		return wire.Response{}, e
	}
	bootstrap := *b.q.Bootstrap
	policy := agentnetwork.Policy{Access: bootstrap.NetworkAccess, AllowedDomains: bootstrap.AllowedDomains}
	domains, _ := json.Marshal(policy.Hosts())
	labels := wire.Labels(b.q.Config, b.q.Reference)
	labels[bootstrapLabel] = "pending"
	live, e := sdk.CreateSandbox(ctx, c.Name,
		sdk.WithImage(b.q.Config.Image), sdk.WithMemory(b.q.Config.MemoryMiB), sdk.WithCPUs(b.q.Config.CPUs),
		sdk.WithMaxMemory(b.q.Config.MemoryMiB), sdk.WithMaxCPUs(b.q.Config.CPUs),
		sdk.WithRootDisk(sdk.RootDisk.Managed(b.q.Config.RootDiskMiB)), sdk.WithUser("1000:1000"),
		// A native owned disk keeps workspace and staging on one filesystem.
		// Bootstrap creates their directories before starting the daemon.
		sdk.WithWorkdir("/"), sdk.WithMounts(map[string]sdk.MountConfig{
			"/environment": sdk.Mount.Owned(sdk.OwnedVolumeOptions{Kind: sdk.VolumeKindDisk, SizeMiB: b.q.Config.EnvironmentDiskMiB}),
		}),
		sdk.WithLabels(labels), sdk.WithDetached(), sdk.WithQuietLogs(), sdk.WithNetwork(b.network()),
		sdk.WithEnv(map[string]string{
			"HOME": "/home/runtime", "OAC_RUNTIME_HOME": "/home/runtime/.oac",
			"OAC_RUNTIME_ENVIRONMENT_ID": bootstrap.EnvironmentID, "OAC_RUNTIME_SESSION_ID": bootstrap.SessionID,
			"OAC_RUNTIME_NETWORK_ACCESS": policy.Access, "OAC_RUNTIME_ALLOWED_DOMAINS": string(domains),
			"OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE": "/run/oac/daemon-suspend.json",
		}))
	if e != nil {
		return wire.Response{}, e
	}
	defer live.Detach(context.Background())
	c.ID = live.ID()
	h, e := sdk.GetSandbox(ctx, c.Name)
	if e != nil {
		return wire.Response{}, e
	}
	qualified, e := qualifyCreatedConfiguration(b.q.Config, b.q.Reference, c, h.ID(), string(h.Status()), h.ConfigJSON())
	if e != nil {
		return qualified, e
	}
	data, e := json.Marshal(bootstrap)
	if e != nil {
		return wire.Response{}, e
	}
	initialization, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, e := runCommand(initialization, live, sandbox.Command{Args: []string{"/usr/bin/python3", "-I", "-S", "-c", bootstrapScript}, Stdin: data}, "0:0")
	if e != nil {
		return wire.Response{}, e
	}
	if result.ExitCode != 0 {
		return wire.Response{}, sandbox.ErrCommandUnconfirmed
	}
	// Persist the final bootstrap receipt without restarting the live guest.
	// v0.7.2 cannot update active labels; ownership reads persisted config.
	_, e = live.Modify(ctx, sdk.ModifyOptions{Labels: map[string]string{bootstrapLabel: "complete"}, Policy: sdk.ModificationPolicyNextStart})
	if e != nil {
		return wire.Response{}, e
	}
	_, state, e := b.inspect(ctx, c)
	if e == nil && !state.BootstrapComplete {
		return wire.Response{}, sandbox.ErrCommandUnconfirmed
	}
	return wire.Response{State: &state}, e
}

// Only the initial post-Create inspection uses this proof. Native creation has
// returned successfully, and no bootstrap command has started. Ordinary inspect
// and unknown Create outcomes cannot acquire settlement through this path.
func qualifyCreatedConfiguration(config wire.Config, ref sandbox.Reference, created wire.Compute, actualID, status, raw string) (wire.Response, error) {
	if created.ID == "" {
		return wire.Response{}, sandbox.ErrOwnership
	}
	state, err := qualifyCompute(config, ref, created, actualID, status, raw)
	if err != nil {
		return wire.Response{}, err
	}
	if state.Status == "" || state.Status == "absent" || state.BootstrapComplete {
		return wire.Response{}, sandbox.ErrOwnership
	}
	if err := qualifyConfiguration(config, created, raw, false); err != nil {
		return wire.Response{State: &state, CreateSettled: true}, err
	}
	return wire.Response{State: &state}, nil
}
