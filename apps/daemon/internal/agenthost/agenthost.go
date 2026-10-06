package agenthost

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Config is the agent host's own configuration, shared by its Sessions. It
// holds a credential: keep it in memory and never log it.
//
// The agent host does not yet recover Sessions that an earlier agent-host
// process left. Until it does, an agent-host process must not reuse the
// StateDir or the UIDs of an earlier one.
type Config struct {
	// StateDir is an absolute host directory private to the agent host. Each
	// Session's directory is StateDir/sessions/<Session ID>, and
	// StateDir/lock is the installation lock.
	StateDir string
	// UIDs is the range Session uids are allocated from; each Session's gid
	// equals its uid. Only one agent host runs per kernel, and nothing else
	// uses the range or starts session views.
	UIDs UIDRange
	// ViewCgroups is the absolute path of a cgroup v2 directory delegated to
	// the agent host, which runs outside it. Each view runs in a cgroup of its
	// own there, and every cgroup there counts as one of this agent host's
	// views. It is unrelated to the Process protocol's cgroup scope, which
	// belongs to the sandbox's process service.
	ViewCgroups string
	// RelayURL and TLS reach the Link relay, as sandboxlink.DialAttach takes
	// them. A nil TLS uses the system roots.
	RelayURL string
	TLS      *tls.Config
	// RuntimeID and Credential authenticate the agent host to the relay.
	RuntimeID  sandboxwire.ID
	Credential []byte
	// Harnesses holds the Harness declarations. A kind runs only when it
	// declares an agent.View.
	Harnesses *agent.Registry
	// Shim is the absolute host path of the static oac-process-shim binary.
	Shim string
	// CADir is an absolute host directory of regular PEM files: the roots the
	// agent host trusts. The gateway trusts exactly these for upstream TLS,
	// and the view presents the directory read-only at the same path.
	CADir string
	// Log receives each view's presentation report. Nil discards it.
	Log *slog.Logger
}

// UIDRange is Count ids from First. First is nonzero.
type UIDRange struct {
	First, Count uint32
}

// Session is one Session the agent host runs.
type Session struct {
	// Binding is the Session's Link attachment.
	Binding Binding
	// Environment is what processes forwarded to the sandbox receive.
	Environment Environment
	// Request is the Session's frozen request. Its Input is ignored; each
	// Turn's input arrives on Input.
	Request proto.PromptRequestPayload
	// Input carries one Turn each. Run runs them in order and ends the
	// Session once Input is closed and the last Turn has settled.
	Input <-chan Input
	// Output receives every Turn's envelopes. Run never closes it.
	Output chan<- proto.Envelope
}

// Binding is the identity of the Session's Link attachment, as each Open
// carries it.
type Binding struct {
	Resource        sandboxlink.ResourceRef
	AttachmentID    sandboxwire.ID
	SessionID       sandboxwire.ID
	AssignmentID    sandboxwire.ID
	AssignmentEpoch uint64
	// AttachGrant authorizes each Open and renewal. It is secret.
	AttachGrant []byte
}

// Environment is the remote environment policy of processes forwarded to the
// sandbox.
type Environment struct {
	// Sandbox holds the Environment's fixed values, such as HOME, PATH,
	// TMPDIR and LANG in the sandbox.
	Sandbox map[string]string
	// Tool is the Environment's tool environment.
	Tool map[string]string
}

// Input is one Turn: its run ID and its input.
type Input struct {
	RunID   string
	Message proto.MessageInput
}

// Error kinds. Every error Open and Run return matches one of them with
// errors.Is.
var (
	// ErrUnsupported is a platform other than Linux, a host that lacks a
	// requirement, or a Session that asks for what the agent host does not
	// run. A missing requirement also matches the sessionview error that
	// names it. A Session's error also matches agent.ErrUnsupportedKind,
	// agent.ErrUnsupportedOperation, agent.ErrViewHandoff, or
	// agent.ErrInvalidView for a view whose paths meet the agent host's own
	// overlays.
	ErrUnsupported = errors.New("agenthost: unsupported")
	// ErrInvalidConfig is a Config that Open or Run rejects.
	ErrInvalidConfig = errors.New("agenthost: invalid configuration")
	// ErrStateLocked means another agent host holds the StateDir's
	// installation lock.
	ErrStateLocked = errors.New("agenthost: state directory in use")
	// ErrInvalidSession is a malformed Session.
	ErrInvalidSession = errors.New("agenthost: invalid session")
	// ErrCapacity means every Session uid is in use.
	ErrCapacity = errors.New("agenthost: no free session uid")
	// ErrSessionExists means the Session's directory already exists.
	ErrSessionExists = errors.New("agenthost: session directory exists")
	// ErrExecutor is a view Executor factory that failed.
	ErrExecutor = errors.New("agenthost: view executor failed")
	// ErrLink is a Link attachment that failed or ended.
	ErrLink = errors.New("agenthost: link attachment failed")
	// ErrWorld is a world that no longer shows the sandbox faithfully, or
	// that cannot show that its attachment holds nothing.
	ErrWorld = errors.New("agenthost: world lost")
	// ErrLaunch is a view that could not be launched.
	ErrLaunch = errors.New("agenthost: launch failed")
	// ErrProcessBroker is a view's process broker that could not start, or
	// whose process relay was lost while the view ran
	// (processbroker.ErrRelayLost).
	ErrProcessBroker = errors.New("agenthost: process broker failed")
	// ErrTurn is a Turn that failed or left its Executor unusable.
	ErrTurn = errors.New("agenthost: turn failed")
	// ErrTeardown is a Session resource that could not be released, such as
	// a view whose teardown did not finish (sessionview.ErrCleanup), or what
	// an earlier agent host left that Open could not recover.
	ErrTeardown = errors.New("agenthost: teardown incomplete")
)

// Host is a running agent host. It holds the installation lock on its
// StateDir from Open until Close.
type Host struct {
	cfg  Config
	lock *os.File
}

// Close releases the installation lock. Call it once every Run has
// returned.
func (h *Host) Close() error {
	return h.lock.Close()
}

// Error is a typed agent host failure. It matches Kind and, when present,
// Err. Its message never includes a credential.
type Error struct {
	Kind error
	Op   string
	Err  error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Kind.Error())
	if e.Op != "" {
		b.WriteString(": " + e.Op)
	}
	if e.Err != nil {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

func (e *Error) Unwrap() []error {
	if e.Err == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Err}
}
