package agenthost

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Config is the agent host's own configuration, shared by its Sessions. It
// holds a credential: keep it in memory and never log it.
type Config struct {
	// StateDir is an absolute host directory private to the agent host. Each
	// Session's directory is StateDir/sessions/<Session ID>.
	StateDir string
	// UIDs is the range Session uids are allocated from; each Session's gid
	// equals its uid. Only one agent host runs per kernel, and nothing else
	// uses the range or starts session views.
	UIDs UIDRange
	// ViewCgroups is the canonical path of the cgroup v2 directory delegated
	// to the agent host for its views. Every cgroup in it counts as a view's.
	// It is unrelated to the Process protocol's cgroup scope.
	ViewCgroups string
	// RelayURL and TLS reach the Link relay, as sandboxlink.DialAttach takes
	// them. A nil TLS uses the system roots.
	RelayURL string
	TLS      *tls.Config
	// RuntimeID and Credential authenticate the agent host to the relay.
	RuntimeID  sandboxwire.ID
	Credential []byte
	// Harnesses holds the Harness declarations as their adapters state
	// them, registered with RegisterKind and RegisterView: Registry composes
	// them with the Environments the agent host serves. A kind runs only
	// when it declares an agent.View.
	Harnesses *agent.Registry
	// Shim is the absolute host path of the static oac-process-shim binary.
	Shim string
	// CADir is an absolute host directory of regular PEM files: the roots the
	// agent host trusts. The gateway trusts exactly these for upstream TLS,
	// and the view presents the directory read-only at the same path.
	CADir string
	// Log receives each view's presentation report and each failure of a
	// Session, which no Turn reports. Nil discards it.
	Log *slog.Logger
}

// UIDRange is Count ids from First. First is nonzero.
type UIDRange struct {
	First, Count uint32
}

// Binding is the Session's Link assignment, as each Open carries it. Each
// Executor of the Session opens its own attachment under it.
type Binding struct {
	Resource        sandboxlink.ResourceRef
	SessionID       sandboxwire.ID
	AssignmentID    sandboxwire.ID
	AssignmentEpoch uint64
	// AttachGrant authorizes each Open and renewal. It is secret.
	AttachGrant []byte
}

// Environment is the remote environment policy of processes forwarded to the
// sandbox.
type Environment struct {
	// Sandbox holds the Environment's fixed values in the sandbox: HOME,
	// PATH and LANG.
	Sandbox map[string]string
	// Tool is the Environment's tool environment.
	Tool map[string]string
}

// Error kinds. Every error that Open, Host.Registry's Executor factory and
// Executors, and Host.RemoveHome return, and every Session failure the agent
// host logs, matches one of them with errors.Is.
var (
	// ErrUnsupported is a platform other than Linux, a host that lacks a
	// requirement, or a Session that asks for what the agent host does not
	// run. A missing requirement also matches the sessionview error that
	// names it. A Session's error also matches agent.ErrUnsupportedKind,
	// agent.ErrUnsupportedOperation, agent.ErrViewHandoff, or
	// agent.ErrInvalidView for a view whose paths meet the agent host's own
	// overlays.
	ErrUnsupported = errors.New("agenthost: unsupported")
	// ErrInvalidConfig is a Config that Open or an Executor factory rejects.
	ErrInvalidConfig = errors.New("agenthost: invalid configuration")
	// ErrStateLocked means another agent host holds the StateDir or the
	// ViewCgroups.
	ErrStateLocked = errors.New("agenthost: state in use")
	// ErrInvalidSession is a malformed request, binding or Environment.
	ErrInvalidSession = errors.New("agenthost: invalid session")
	// ErrCapacity means every Session uid is in use.
	ErrCapacity = errors.New("agenthost: no free session uid")
	// ErrSessionExists means an Executor of the Session has not closed, or
	// Host.RemoveHome is removing the Session's directory.
	ErrSessionExists = errors.New("agenthost: session in use")
	// ErrExecutor is a view Executor factory that failed.
	ErrExecutor = errors.New("agenthost: view executor failed")
	// ErrLink is a Link attachment that failed or ended.
	ErrLink = errors.New("agenthost: link attachment failed")
	// ErrWorld is a world that no longer shows the sandbox faithfully, or
	// that cannot show that its attachment holds nothing.
	ErrWorld = errors.New("agenthost: world lost")
	// ErrLaunch is a view, or a process in a view, that could not be started.
	ErrLaunch = errors.New("agenthost: launch failed")
	// ErrProcessBroker is a view's process broker that could not start, or
	// whose process relay was lost while the view ran
	// (processbroker.ErrRelayLost).
	ErrProcessBroker = errors.New("agenthost: process broker failed")
	// ErrTeardown is a Session resource that could not be released, such as
	// a view whose teardown did not finish (sessionview.ErrCleanup), or what
	// an earlier agent host left that Open could not recover.
	ErrTeardown = errors.New("agenthost: teardown incomplete")
)

// Host is an agent host that Open has started.
type Host struct {
	cfg Config
	// state and views hold the locks; nil until taken.
	state, views *os.File
	owners       owners
}

// Close releases the installation locks. Call it once every Executor has
// closed.
func (h *Host) Close() error {
	var errs []error
	for _, f := range []*os.File{h.state, h.views} {
		if f != nil {
			errs = append(errs, f.Close())
		}
	}
	return errors.Join(errs...)
}
