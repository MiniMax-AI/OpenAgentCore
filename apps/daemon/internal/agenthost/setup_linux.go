//go:build linux

package agenthost

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// initializationGrace is how long a cancelled setup step may run before it is
// killed.
const initializationGrace = 250 * time.Millisecond

// strongestScope is the strongest process scope caps declare.
func strongestScope(caps sp.Capabilities) (sp.Scope, error) {
	for _, scope := range []sp.Scope{sp.ScopeCgroupV2, sp.ScopePOSIXSession} {
		if slices.Contains(caps.Scopes, scope) {
			return scope, nil
		}
	}
	return 0, fmt.Errorf("the Process service declares no scope among %v", caps.Scopes)
}

// run runs one setup step in the sandbox as the Process operation id, in the
// strongest scope the service declares, discards its output and returns once
// it has settled: nil when it exited 0, an InitializationFailure when it
// could not start or exited 1 to 255, dispatch.ErrEnvironmentUnavailable when
// the service is unreachable or refused it before any effect, and an error
// otherwise. When ctx ends first, run cancels the step and waits closeBound
// for it to settle. A step that may run unobserved quarantines the owner.
func (o *environment) run(ctx context.Context, id sandboxwire.ID, program string, args []string, env map[string]string, cwd string) error {
	rw, err := o.link.open(ctx, sandboxlink.ServiceProcess, sp.Version)
	if err != nil {
		return dispatch.ErrEnvironmentUnavailable
	}
	c := sp.NewClient(rw)
	defer c.Close()
	d, err := c.Describe(ctx)
	if err != nil {
		return dispatch.ErrEnvironmentUnavailable
	}
	scope, err := strongestScope(d.Capabilities)
	if err != nil {
		return dispatch.ErrEnvironmentUnavailable
	}
	spec := sp.ProcessSpec{Executable: []byte(program), Argv: [][]byte{[]byte(program)}, Cwd: []byte(cwd), Umask: 0o022, IOMode: sp.IOPipes, Scope: scope}
	for _, arg := range args {
		spec.Argv = append(spec.Argv, []byte(arg))
	}
	for _, name := range slices.Sorted(maps.Keys(env)) {
		spec.Env = append(spec.Env, sp.EnvVar{Name: []byte(name), Value: []byte(env[name])})
	}
	start := sp.StartRequest{OperationRef: sp.OperationRef{ServerInstanceID: d.ServerInstanceID, OperationID: id}, Spec: spec}
	if spec.Validate() != nil || d.Capabilities.CheckStart(spec) != nil || len(sp.Encode(start)) > int(d.Capabilities.MaxStartBytes) {
		return agentcapabilities.ErrInvalid
	}
	op, _, err := c.Start(ctx, d.ServerInstanceID, id, spec)
	if err != nil {
		var f *sp.Failure
		if errors.As(err, &f) && f.Effect == sandboxwire.EffectNone {
			return dispatch.ErrEnvironmentUnavailable
		}
		return o.lose(err)
	}
	// late bounds the requests that settle the step: closeBound past ctx.
	late, cancelLate := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelLate()
	defer context.AfterFunc(ctx, func() { time.AfterFunc(closeBound, cancelLate) })()
	var exit *sp.ExitStatus
	var exitLost, startFailed, outputClosed, scopeClosed bool
	cancelled := ctx.Done()
	for !startFailed && !((exit != nil || exitLost) && outputClosed && scopeClosed) {
		select {
		case ev, ok := <-op.Events():
			if !ok {
				return o.lose(c.Err())
			}
			switch ev := ev.(type) {
			case sp.StartFailedEvent:
				startFailed = true
			case sp.ExitedEvent:
				exit = &ev.Status
			case sp.ObservationLostEvent:
				exitLost = exitLost || ev.Observation == sp.ObservationExit
			case sp.OutputClosedEvent:
				outputClosed = true
			case sp.ScopeClosedEvent:
				scopeClosed = true
			}
			if err := op.Ack(late, ev.Header().Sequence); err != nil {
				return o.lose(err)
			}
		case <-cancelled:
			cancelled = nil
			if err := op.Cancel(late, uint32(initializationGrace/time.Millisecond)); err != nil {
				return o.lose(err)
			}
		case <-late.Done():
			return o.lose(ctx.Err())
		}
	}
	if op.Release(late) != nil {
		op.Detach()
	}
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("setup step cancelled: %w", ctx.Err())
	case startFailed:
		return &dispatch.InitializationFailure{}
	case exitLost || exit.Kind != sp.ExitCode:
		return errors.New("the setup step's exit status is unknown")
	case exit.Code != 0:
		code := int(exit.Code)
		return &dispatch.InitializationFailure{ExitCode: &code}
	}
	return nil
}

// lose quarantines the owner for a setup step that may still run and whose
// outcome it cannot observe.
func (o *environment) lose(err error) error {
	o.uncertain = true
	return fmt.Errorf("%w: setup step: %w", errUncertain, err)
}
