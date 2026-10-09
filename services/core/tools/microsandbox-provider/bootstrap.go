//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

// The bootstrap script writes the Sandbox I/O service's input, read from
// stdin so the credential never appears in arguments, and starts the service
// as the sandbox user.
const bootstrapScript = `
import ctypes,os,stat,subprocess,sys
from pathlib import Path
data=sys.stdin.buffer.read()
for p in ['/home/runtime','/environment','/environment/workspace','/environment/initialization','/environment/packages']:
    os.makedirs(p,mode=0o700,exist_ok=True)
    if not stat.S_ISDIR(os.lstat(p).st_mode): raise RuntimeError('invalid bootstrap directory')
    os.chmod(p,0o700);os.chown(p,1000,1000)
s='/home/runtime/sandbox-io-bootstrap.json'
fd=os.open(s,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
with os.fdopen(fd,'wb') as f:
    f.write(data);f.flush();os.fsync(f.fileno());os.fchown(f.fileno(),1000,1000)
if not stat.S_ISDIR(os.lstat('/workspace').st_mode): raise RuntimeError('invalid workspace alias')
libc=ctypes.CDLL(None,use_errno=True)
if libc.mount(b'/environment/workspace',b'/workspace',None,4096,None)!=0:
    raise OSError(ctypes.get_errno(),'workspace bind mount failed')
mounts=Path('/proc/self/mountinfo').read_text().splitlines()
if not any(line.split(' - ')[0].split()[4]=='/sys/fs/cgroup'
           and line.split(' - ')[1].split()[0]=='cgroup2' for line in mounts):
    raise RuntimeError('Sandbox process containment requires cgroup v2')
membership=[line[3:] for line in Path('/proc/self/cgroup').read_text().splitlines() if line.startswith('0::/')]
if len(membership)!=1 or '..' in Path(membership[0]).parts:
    raise RuntimeError('Invalid sandbox cgroup membership')
parent=Path('/sys/fs/cgroup')/membership[0].lstrip('/')
if str(os.getpid()) not in (parent/'cgroup.procs').read_text().splitlines():
    raise RuntimeError('Sandbox cgroup mount does not match membership')
group=parent/'oac-sandbox-io'
# Keep the VM's resource ancestors; never reuse an uncertain startup's group.
group.mkdir(mode=0o700)
if (group/'cgroup.subtree_control').read_text().strip():
    raise RuntimeError('Sandbox process group has active controllers')
(group/'cgroup.kill').write_text('1')
group.chmod(0o700);os.chown(group,1000,1000)
os.chown(group/'cgroup.procs',1000,1000);(group/'cgroup.procs').chmod(0o600)
def sandbox_user():
    (group/'cgroup.procs').write_text(str(os.getpid()))
    os.setgroups([]);os.setgid(1000);os.setuid(1000)
subprocess.Popen(['/usr/local/bin/oac-sandbox-io','--bootstrap-file',s],env={},start_new_session=True,
                 stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,
                 cwd='/environment/workspace',preexec_fn=sandbox_user)
`

func (b backend) create(ctx context.Context) (wire.Response, error) {
	c := wire.Compute{Name: wire.Name(b.q.Config, b.q.Reference, 0)}
	if _, _, e := b.inspect(ctx, c); e == nil {
		return wire.Response{}, sandbox.ErrExists
	} else if !sdk.IsKind(e, sdk.ErrSandboxNotFound) {
		return wire.Response{}, e
	}
	input, e := b.q.Bootstrap.SandboxIO.Marshal()
	if e != nil {
		return wire.Response{}, sandbox.ErrInvalid
	}
	labels := wire.Labels(b.q.Config, b.q.Reference)
	labels[bootstrapLabel] = "pending"
	live, e := sdk.CreateSandbox(ctx, c.Name,
		sdk.WithImage(b.q.Config.Image), sdk.WithMemory(b.q.Config.MemoryMiB), sdk.WithCPUs(b.q.Config.CPUs),
		sdk.WithMaxMemory(b.q.Config.MemoryMiB), sdk.WithMaxCPUs(b.q.Config.CPUs),
		sdk.WithRootDisk(sdk.RootDisk.Managed(b.q.Config.RootDiskMiB)), sdk.WithUser("1000:1000"),
		// The Environment lives on a native owned disk; bootstrap creates its
		// directories before starting Sandbox I/O.
		sdk.WithWorkdir("/"), sdk.WithMounts(map[string]sdk.MountConfig{
			"/environment": sdk.Mount.Owned(sdk.OwnedVolumeOptions{Kind: sdk.VolumeKindDisk, SizeMiB: b.q.Config.EnvironmentDiskMiB}),
		}),
		sdk.WithLabels(labels), sdk.WithDetached(), sdk.WithQuietLogs(), sdk.WithNetwork(b.network()))
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
	initialization, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if e = runBootstrap(initialization, live, input); e != nil {
		return wire.Response{}, e
	}
	// Persist the final bootstrap receipt without restarting the live guest.
	// v0.7.2 cannot update active labels; ownership reads persisted config.
	_, e = live.Modify(ctx, sdk.ModifyOptions{Labels: map[string]string{bootstrapLabel: "complete"}, Policy: sdk.ModificationPolicyNextStart})
	if e != nil {
		return wire.Response{}, e
	}
	_, state, e := b.inspect(ctx, c)
	if e == nil && !state.BootstrapComplete {
		return wire.Response{}, sandbox.ErrComputeUnconfirmed
	}
	return wire.Response{State: &state}, e
}

// runBootstrap runs the bootstrap script as root with input on its stdin.
// Only a confirmed zero exit after the whole input was written settles it.
func runBootstrap(ctx context.Context, live *sdk.Sandbox, input []byte) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return sandbox.ErrInvalid
	}
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return sandbox.ErrComputeUnconfirmed
	}
	handle, e := live.ExecStream(ctx, "/usr/bin/python3", []string{"-I", "-S", "-c", bootstrapScript},
		sdk.WithExecUser("0:0"), sdk.WithExecTimeout(timeout), sdk.WithExecStdinPipe())
	if e != nil {
		return errors.Join(sandbox.ErrComputeUnconfirmed, e)
	}
	defer handle.Close()
	sink := handle.TakeStdin()
	if sink == nil {
		return sandbox.ErrComputeUnconfirmed
	}
	// Write and receive concurrently to avoid full-pipe deadlocks.
	written := make(chan error, 1)
	go func() {
		n, err := sink.WriteCtx(ctx, input)
		if err == nil && n != len(input) {
			err = io.ErrShortWrite
		}
		if err == nil {
			err = sink.Close()
		}
		written <- err
	}()
	return awaitBootstrap(ctx, handle.Recv, written)
}

func awaitBootstrap(ctx context.Context, receive func(context.Context) (*sdk.ExecEvent, error), written <-chan error) error {
	exited, code := false, 0
	for {
		event, err := receive(ctx)
		if err != nil {
			return errors.Join(sandbox.ErrComputeUnconfirmed, err)
		}
		switch event.Kind {
		case sdk.ExecEventExited:
			if exited {
				return sandbox.ErrComputeUnconfirmed
			}
			exited, code = true, event.ExitCode
		case sdk.ExecEventStdinError, sdk.ExecEventFailed:
			return sandbox.ErrComputeUnconfirmed
		case sdk.ExecEventDone:
			if !exited || code != 0 {
				return sandbox.ErrComputeUnconfirmed
			}
			select {
			case err := <-written:
				if err != nil {
					return sandbox.ErrComputeUnconfirmed
				}
				return nil
			case <-ctx.Done():
				return sandbox.ErrComputeUnconfirmed
			}
		}
	}
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
