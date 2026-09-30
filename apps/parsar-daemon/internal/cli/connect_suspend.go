package cli

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/authoring"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/transport"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

const suspendReconnectTimeout = 30 * time.Second

// The bridge and router share this sender across the one explicitly planned
// reconnect. Quiesce has drained every prior send before replace is called.
type reconnectSender struct {
	mu   sync.RWMutex
	conn *transport.Conn
}

func (s *reconnectSender) Send(ctx context.Context, env proto.Envelope) error {
	s.mu.RLock()
	conn := s.conn
	s.mu.RUnlock()
	return conn.Send(ctx, env)
}
func (s *reconnectSender) replace(conn *transport.Conn) { s.mu.Lock(); s.conn = conn; s.mu.Unlock() }

type suspendedRouter struct {
	router   *dispatch.Router
	bridge   *authoring.Bridge
	sender   *reconnectSender
	registry *agent.Registry
	local    *localworkspace.Binding
}

func newSuspendedRouter(conn *transport.Conn, registry *agent.Registry) (*suspendedRouter, error) {
	local, err := localworkspace.Load()
	if err != nil {
		return nil, err
	}
	sender := &reconnectSender{conn: conn}
	bridge := authoring.New(sender)
	wrapped := authoringRegistry(registry, bridge)
	router, err := dispatch.New(dispatch.Config{Registry: wrapped, Sender: sender, Log: obslog.Bg(), LocalWorkspace: local})
	if err != nil {
		return nil, err
	}
	return &suspendedRouter{router: router, bridge: bridge, sender: sender, registry: wrapped, local: local}, nil
}

func (s *suspendedRouter) shutdown() {
	shutdownRouterUntilConfirmed(s.router.Shutdown, time.Second)
}

// runSuspendLoop uses the ordinary connection authentication and dispatch chain.
// Only an acknowledged, fully drained suspension retains a Router across sockets.
func runSuspendLoop(ctx context.Context, dial transport.DialFn, registry *agent.Registry, boot *transport.BootstrapResponse, discovery agentCLIDiscovery, control *suspendControl) error {
	for ctx.Err() == nil {
		conn, err := transport.Reconnect(ctx, dial, transport.DefaultBackoff, nil)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		state, err := newSuspendedRouter(conn, registry)
		if err != nil {
			_ = conn.Close()
			return err
		}
		err = serveSuspendLifecycle(ctx, conn, dial, state, boot, discovery, control)
		state.shutdown()
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, transport.ErrPermanent) {
			return err
		}
		if err != nil {
			obslog.Bg().Warn("hosted connection ended", "err", err)
		}
		if err := transport.Sleep(ctx, time.Second); err != nil {
			return nil
		}
	}
	return nil
}

func serveSuspendLifecycle(ctx context.Context, conn *transport.Conn, dial transport.DialFn, state *suspendedRouter, boot *transport.BootstrapResponse, discovery agentCLIDiscovery, control *suspendControl) error {
	defer func() { _ = conn.Close() }()
	for {
		request, err := state.pump(ctx, conn, boot, discovery, control)
		_ = conn.Close()
		if err != nil || request == nil {
			return err
		}
		// There is deliberately no pre-snapshot wall-clock deadline here. A clock
		// jump on restore must not end the park before the host's wake command.
		// Only this exact armed suspension can park; Core owns its snapshot TTL.
		if err := control.Wait(ctx); err != nil {
			return err
		}
		next, err := state.reconnectSuspension(ctx, dial, *request, control, suspendReconnectTimeout)
		if err != nil {
			if next != nil {
				_ = next.Close()
			}
			// Once Resume commits, a failed acknowledgement is an ordinary
			// disconnect. Keep only its receipt for the fresh authenticated Router.
			if control.lastResumed != nil && control.lastResumed.SameSuspension(*request) {
				return errors.Join(err, control.Disarm())
			}
			return err
		}
		conn = next
		if err := control.Disarm(); err != nil {
			return err
		}
	}
}

// Each attempt has a deadline, but transient transport failures cannot discard
// the armed suspension. Only matching Core confirmation opens admission again.
func (s *suspendedRouter) reconnectSuspension(ctx context.Context, dial transport.DialFn, request proto.EnvironmentSuspendPayload, control *suspendControl, timeout time.Duration) (*transport.Conn, error) {
	for attempt := 1; ; attempt++ {
		recovery, cancel := context.WithTimeout(ctx, timeout)
		conn, err := transport.Reconnect(recovery, dial, transport.DefaultBackoff, nil)
		if err == nil {
			err = s.resume(recovery, conn, request, control)
		}
		cancel()
		if err == nil {
			return conn, nil
		}
		if conn != nil {
			_ = conn.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, transport.ErrPermanent) || (control.lastResumed != nil && control.lastResumed.SameSuspension(request)) {
			return nil, err
		}
		if err := transport.Sleep(ctx, transport.DefaultBackoff.Delay(attempt)); err != nil {
			return nil, err
		}
	}
}

func (s *suspendedRouter) heartbeats(ctx context.Context, conn *transport.Conn, boot *transport.BootstrapResponse, discovery agentCLIDiscovery) {
	conn.StartHeartbeats(ctx, boot.HeartbeatInterval(), func() proto.HeartbeatPayload {
		kinds := s.registry.SupportedAgentKinds()
		for i := range kinds {
			caps := &kinds[i].Capabilities
			caps.WorkspaceOutputExport = proto.CapabilityFromBool(s.local.CanExport() && caps.LocalEnvironment.IsSupported() && caps.WorkspaceReadPreparation.IsSupported())
		}
		return proto.HeartbeatPayload{Timestamp: time.Now().Unix(), ActiveRequests: s.router.ActiveRuns(), DaemonVersion: Version, SupportedAgentKinds: kinds}
	}, obslog.Bg())
}

func (s *suspendedRouter) pump(ctx context.Context, conn *transport.Conn, boot *transport.BootstrapResponse, discovery agentCLIDiscovery, control *suspendControl) (*proto.EnvironmentSuspendPayload, error) {
	s.heartbeats(ctx, conn, boot, discovery)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-conn.Done():
			return nil, conn.Err()
		case env, ok := <-conn.Recv():
			if !ok {
				return nil, conn.Err()
			}
			if env.Type == proto.TypeAuthoringResponse {
				s.bridge.Deliver(env)
				continue
			}
			if env.Type == proto.TypeEnvironmentResume {
				request := rejectedResumeRequest(env)
				code := "not_suspended"
				valid := env.ID != "" && len(env.ID) <= 128 && request.EnvironmentID == control.identity.EnvironmentID && request.SuspendID != "" && len(request.SuspendID) <= 128
				if valid && ((control.lastResumed != nil && control.lastResumed.SameSuspension(request)) || request.Rollback) {
					code = ""
					control.lastResumed = &request
				}
				if err := sendSuspendResult(ctx, conn, env, proto.TypeEnvironmentResumed, request, code); err != nil {
					return nil, err
				}
				continue
			}
			if env.Type == proto.TypeEnvironmentQuiesce {
				var request proto.EnvironmentSuspendPayload
				if env.ID == "" || len(env.ID) > 128 || env.DecodePayload(&request) != nil || request.EnvironmentID != control.identity.EnvironmentID || request.SuspendID == "" || request.Rollback {
					if err := sendSuspendResult(ctx, conn, env, proto.TypeEnvironmentQuiesced, request, "invalid_request"); err != nil {
						return nil, err
					}
					continue
				}
				quiet, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := s.router.Quiesce(quiet, request)
				cancel()
				if err != nil {
					if sendErr := sendSuspendResult(ctx, conn, env, proto.TypeEnvironmentQuiesced, request, "resource_busy"); sendErr != nil {
						return nil, sendErr
					}
					if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
						return nil, err
					}
					continue
				}
				if err := control.Arm(request); err != nil {
					return nil, err
				}
				if err := sendSuspendResult(ctx, conn, env, proto.TypeEnvironmentQuiesced, request, ""); err != nil {
					return nil, err
				}
				return &request, nil
			}
			if err := s.router.Handle(ctx, env); err != nil {
				obslog.Bg().Error("router.Handle failed", "type", env.Type, "err", err)
			}
		}
	}
}

func (s *suspendedRouter) resume(ctx context.Context, conn *transport.Conn, request proto.EnvironmentSuspendPayload, control *suspendControl) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-conn.Done():
		if err := conn.Err(); err != nil {
			return err
		}
		return transport.ErrConnClosed
	case env, ok := <-conn.Recv():
		if !ok {
			if err := conn.Err(); err != nil {
				return err
			}
			return transport.ErrConnClosed
		}
		var echoed proto.EnvironmentSuspendPayload
		if env.Type != proto.TypeEnvironmentResume || env.ID == "" || env.DecodePayload(&echoed) != nil || !echoed.SameSuspension(request) {
			return errors.Join(transport.ErrPermanent, errors.New("connect: expected authenticated suspension resume"))
		}
		s.sender.replace(conn)
		if err := s.router.Resume(echoed, s.sender); err != nil {
			return errors.Join(transport.ErrPermanent, err)
		}
		control.lastResumed = &request
		return sendSuspendResult(ctx, conn, env, proto.TypeEnvironmentResumed, request, "")
	}
}

func sendSuspendResult(ctx context.Context, conn *transport.Conn, env proto.Envelope, kind string, request proto.EnvironmentSuspendPayload, code string) error {
	result, err := proto.NewEnvelope(kind, env.ID, proto.EnvironmentSuspendResultPayload{EnvironmentID: request.EnvironmentID, SuspendID: request.SuspendID, Accepted: code == "", ErrorCode: code})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Send(ctx, result)
}

func rejectedResumeRequest(env proto.Envelope) proto.EnvironmentSuspendPayload {
	var request proto.EnvironmentSuspendPayload
	if len(env.Payload) <= 1024 {
		_ = env.DecodePayload(&request)
	}
	return request
}
