package processbroker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

// Config configures one Session's broker.
type Config struct {
	// RunDir is the host directory the view binds as its run directory,
	// the private directory agent.ViewRunName.
	// It must be owned by the broker's user and writable by no one else, and
	// the view needs no write access to it: a shim only connects.
	RunDir string
	// UID is the view's uid; only its processes may connect.
	UID int
	// PTSDevice is the device number of the view's devpts instance,
	// sessionview.PTS.Device. A terminal is accepted only from it.
	PTSDevice uint64
	// Executables is the declared executable table.
	Executables Executables
	// Environment is the declared environment policy.
	Environment Environment
	// Scope is the containment each operation starts in. The service must
	// declare it.
	Scope sp.Scope
	// Dial opens a Process stream to the Session's sandbox.
	Dial func(context.Context) (io.ReadWriteCloser, error)
	// CancelGrace is the Cancel grace when a shim is lost before its program
	// exits. The service caps it at its CancelGraceLimitMillis.
	CancelGrace time.Duration
	// Logger receives the broker's decisions; nil uses slog.Default.
	Logger *slog.Logger
}

// Executables maps local invocation paths to remote executables. A remote
// executable is a bare name, resolved on the remote PATH, or an absolute
// sandbox path.
type Executables struct {
	// Names maps a name in the view's shim directory to its remote executable.
	Names map[string]string
	// Paths maps an absolute view path the shim is bound over, such as
	// /bin/bash, to its remote executable.
	Paths map[string]string
}

// Environment is the remote environment policy. A name set in more than one
// place takes the value from the later of Pass, Sandbox and Tool. Nothing
// else reaches the sandbox.
type Environment struct {
	// Pass names the shim environment entries that pass through.
	Pass []string
	// Sandbox holds fixed sandbox values such as HOME, PATH, TMPDIR and LANG.
	Sandbox map[string]string
	// Tool is the Environment's tool environment.
	Tool map[string]string
}

// ErrInvalidConfig wraps every configuration error.
var ErrInvalidConfig = errors.New("processbroker: invalid configuration")

func (c *Config) validate() error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, args...))
	}
	switch {
	case !path.IsAbs(c.RunDir):
		return invalid("run directory %q is not absolute", c.RunDir)
	case c.UID < 0:
		return invalid("uid %d", c.UID)
	case c.PTSDevice == 0:
		return invalid("no devpts device")
	case !c.Scope.Valid():
		return invalid("scope %d", c.Scope)
	case c.Dial == nil:
		return invalid("no dial function")
	case c.CancelGrace < 0:
		return invalid("negative cancel grace")
	}
	names := slices.Concat(c.Environment.Pass, slices.Collect(maps.Keys(c.Environment.Sandbox)), slices.Collect(maps.Keys(c.Environment.Tool)))
	for _, name := range names {
		if !validEnvName(name) {
			return invalid("environment name %q", name)
		}
	}
	_, path1 := c.Environment.Sandbox["PATH"]
	_, path2 := c.Environment.Tool["PATH"]
	for name, remote := range c.Executables.Names {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
			return invalid("executable name %q", name)
		}
		if err := checkRemote(remote, path1 || path2); err != nil {
			return invalid("executable %q: %v", name, err)
		}
	}
	for local, remote := range c.Executables.Paths {
		if !path.IsAbs(local) || path.Clean(local) != local || underPrivate(local) || strings.Contains(local, "\x00") {
			return invalid("executable path %q", local)
		}
		if err := checkRemote(remote, path1 || path2); err != nil {
			return invalid("executable %q: %v", local, err)
		}
	}
	return nil
}

func checkRemote(remote string, havePATH bool) error {
	switch {
	case remote == "" || strings.Contains(remote, "\x00"):
		return errors.New("empty remote executable")
	case strings.Contains(remote, "/") && !path.IsAbs(remote):
		return fmt.Errorf("remote executable %q is neither a name nor absolute", remote)
	case !strings.Contains(remote, "/") && !havePATH:
		return fmt.Errorf("remote name %q needs PATH in the sandbox or tool environment", remote)
	}
	return nil
}

func validEnvName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "=\x00")
}

func underPrivate(p string) bool {
	return p == agent.ViewPrivateRoot || strings.HasPrefix(p, agent.ViewPrivateRoot+"/")
}

// resolve returns the remote executable for the shim's exec path. A relative
// path resolves against cwd; resolution is lexical, because the view's
// symlinks are not the broker's to follow.
func (x Executables) resolve(execPath, cwd string) (string, bool) {
	p := execPath
	if !path.IsAbs(p) {
		p = path.Join(cwd, p)
	}
	p = path.Clean(p)
	if name, ok := strings.CutPrefix(p, agent.ViewPrivateRoot+"/"+agent.ViewShimName+"/"); ok {
		remote, ok := x.Names[name]
		return remote, ok
	}
	remote, ok := x.Paths[p]
	return remote, ok
}

// privateMarker is the view prefix no value may carry into the sandbox.
var privateMarker = []byte(agent.ViewPrivateRoot)

// compose builds the remote environment from the shim's environ. It returns
// the names it dropped because their value names the private directory.
func (e Environment) compose(environ [][]byte) (env []sp.EnvVar, dropped []string) {
	values := map[string][]byte{}
	for _, entry := range environ {
		name, value, ok := bytes.Cut(entry, []byte("="))
		if !ok || !slices.Contains(e.Pass, string(name)) {
			continue
		}
		if _, seen := values[string(name)]; !seen { // getenv returns the first
			values[string(name)] = value
		}
	}
	for name, value := range e.Sandbox {
		values[name] = []byte(value)
	}
	for name, value := range e.Tool {
		values[name] = []byte(value)
	}
	for _, name := range slices.Sorted(maps.Keys(values)) {
		if bytes.Contains(values[name], privateMarker) {
			dropped = append(dropped, name)
			continue
		}
		env = append(env, sp.EnvVar{Name: []byte(name), Value: values[name]})
	}
	return env, dropped
}
