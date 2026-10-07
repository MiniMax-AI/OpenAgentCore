package processbroker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

// Config configures one Session's broker.
type Config struct {
	// Relay is the broker's end of the connection to the Session's process
	// relay, sessionview's View.Relay. The broker uses a duplicate; the
	// caller keeps Relay.
	Relay *os.File
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
	// Aliases maps a name in the view's shim directory to the command it
	// runs. Nothing of the shim's argv, working directory or environment
	// reaches that command.
	Aliases map[string]Command
}

// Command is an alias's frozen command. Its executable may also be a path
// relative to Dir, which is an absolute sandbox path.
type Command struct {
	Executable string
	// Args follow argv[0], which is Executable.
	Args []string
	Dir  string
}

// Environment is the remote environment policy. A name set in more than one
// place takes the value from the later of Pass, Sandbox and Tool. Nothing
// else reaches the sandbox.
type Environment struct {
	// Pass names the shim environment entries that pass through.
	Pass []string
	// Sandbox holds fixed sandbox values: HOME, PATH and LANG.
	Sandbox map[string]string
	// Tool is the Environment's tool environment.
	Tool map[string]string
}

var (
	// ErrInvalidConfig wraps every configuration error.
	ErrInvalidConfig = errors.New("processbroker: invalid configuration")
	// ErrRelayLost wraps the end of the relay connection before Close, or a
	// relay message that breaks the IPC. The broker then serves nothing
	// more, and the Session fails.
	ErrRelayLost = errors.New("processbroker: process relay lost")
)

func (c *Config) validate() error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, args...))
	}
	switch {
	case c.Relay == nil:
		return invalid("no relay connection")
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
		if !validName(name) {
			return invalid("executable name %q", name)
		}
		if err := checkRemote(remote, path1 || path2); err != nil {
			return invalid("executable %q: %v", name, err)
		}
	}
	for name, cmd := range c.Executables.Aliases {
		switch {
		case !validName(name):
			return invalid("alias name %q", name)
		case !path.IsAbs(cmd.Dir):
			return invalid("alias %q: working directory %q is not absolute", name, cmd.Dir)
		case cmd.Executable == "":
			return invalid("alias %q: empty executable", name)
		case !strings.Contains(cmd.Executable, "/") && !(path1 || path2):
			return invalid("alias %q: remote name %q needs PATH in the sandbox or tool environment", name, cmd.Executable)
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

// validName reports whether name can be a file in the view's shim directory.
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

func validEnvName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "=\x00")
}

func underPrivate(p string) bool {
	return p == agent.ViewPrivateRoot || strings.HasPrefix(p, agent.ViewPrivateRoot+"/")
}

// resolve returns the remote executable for the shim's exec path, and the
// frozen command when the path is an alias. A relative path resolves against
// cwd; resolution is lexical, because the view's symlinks are not the
// broker's to follow.
func (x Executables) resolve(execPath, cwd string) (string, *Command, bool) {
	p := execPath
	if !path.IsAbs(p) {
		p = path.Join(cwd, p)
	}
	p = path.Clean(p)
	if name, ok := strings.CutPrefix(p, agent.ViewPrivateRoot+"/"+agent.ViewShimName+"/"); ok {
		if cmd, ok := x.Aliases[name]; ok {
			return cmd.Executable, &cmd, true
		}
		remote, ok := x.Names[name]
		return remote, nil, ok
	}
	remote, ok := x.Paths[p]
	return remote, nil, ok
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
