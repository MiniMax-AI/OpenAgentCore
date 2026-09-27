package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const suspendControlEnv = "OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE"

type suspendIdentity struct {
	PID           int    `json:"pid"`
	StartTime     string `json:"start_time"`
	EnvironmentID string `json:"environment_id"`
	SuspendID     string `json:"suspend_id"`
}

type suspendControl struct {
	// Keep one consumed token across ordinary socket failures so Core can
	// retry its acknowledgement. Arm replaces it before the next suspension.
	lastResumed *proto.EnvironmentSuspendPayload
	path        string
	identity    suspendIdentity
	signal      chan os.Signal
}

func newSuspendControl() (*suspendControl, error) {
	path := os.Getenv(suspendControlEnv)
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("connect: absolute suspend control file required")
	}
	info, err := os.Lstat(filepath.Dir(path))
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("connect: private suspend control directory required")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Getuid()) {
		return nil, errors.New("connect: owned suspend control directory required")
	}
	environment := os.Getenv("OAC_RUNTIME_ENVIRONMENT_ID")
	if environment == "" {
		return nil, errors.New("connect: suspend control requires an Environment binding")
	}
	start, err := suspendProcessStart(os.Getpid())
	if err != nil {
		return nil, err
	}
	c := &suspendControl{path: path, identity: suspendIdentity{PID: os.Getpid(), StartTime: start, EnvironmentID: environment}, signal: make(chan os.Signal, 1)}
	// Refuse a second owner. A new microVM must not adopt another live daemon.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("connect: create suspend control: %w", err)
	}
	err = json.NewEncoder(f).Encode(c.identity)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(path)
		return nil, errors.New("connect: write suspend control failed")
	}
	signal.Notify(c.signal, syscall.SIGUSR1)
	return c, nil
}

func (c *suspendControl) Arm(request proto.EnvironmentSuspendPayload) error {
	if request.EnvironmentID != c.identity.EnvironmentID || request.SuspendID == "" {
		return errors.New("suspension identity mismatch")
	}
	// Signals delivered while serving cannot authorize a future suspension.
	for {
		select {
		case <-c.signal:
			continue
		default:
		}
		break
	}
	c.lastResumed = nil
	c.identity.SuspendID = request.SuspendID
	return c.save()
}

func (c *suspendControl) save() error {
	f, err := os.CreateTemp(filepath.Dir(c.path), ".suspend-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(c.identity)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), c.path)
}

func (c *suspendControl) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.signal:
		return nil
	}
}

func (c *suspendControl) Disarm() error { c.identity.SuspendID = ""; return c.save() }
func (c *suspendControl) Close()        { signal.Stop(c.signal); _ = os.Remove(c.path) }

func runResume(_ *runContext, args []string) error {
	flags := newFlagSet("resume")
	path := flags.String("control-file", "", "absolute hosted daemon control file")
	environment := flags.String("environment-id", "", "expected Environment identity")
	suspension := flags.String("suspend-id", "", "expected suspension identity")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *environment == "" || *suspension == "" {
		return errors.New("resume: Environment and suspension identities required")
	}
	raw, err := readEnvironmentPrivateFile(*path)
	if err != nil {
		return errors.New("resume: private control file unavailable")
	}
	var identity suspendIdentity
	if decodeEnvironmentJSON(raw, &identity) != nil || identity.PID <= 0 || identity.StartTime == "" || identity.EnvironmentID != *environment || identity.SuspendID != *suspension {
		return errors.New("resume: suspension identity mismatch")
	}
	return signalSuspendedProcess(identity)
}
