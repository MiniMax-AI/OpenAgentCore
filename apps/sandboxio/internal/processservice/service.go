//go:build linux

// Package processservice is the Linux process service of the sandbox I/O
// service. It implements the process protocol (internal/sandboxprocess),
// starting each operation in a new POSIX session.
//
// The service binary's main calls Init first and runs Reap for the life of
// the process; see both.
package processservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Config holds the service limits it advertises.
type Config struct {
	MaxActiveOperations        int
	MaxOperationRecords        int
	MaxReplayBytesPerOperation int
	OwnerLossGrace             time.Duration
	CancelGraceLimit           time.Duration
}

// DefaultConfig returns the standard limits.
func DefaultConfig() Config {
	return Config{
		MaxActiveOperations:        256,
		MaxOperationRecords:        65536,
		MaxReplayBytesPerOperation: 8 << 20,
		OwnerLossGrace:             time.Minute,
		CancelGraceLimit:           30 * time.Second,
	}
}

// signals are the signals the service delivers.
var signals = []sp.Signal{1, 2, 3, 9, 10, 12, 14, 15, 18, 19, 20, 21, 22, 28} // HUP INT QUIT KILL USR1 USR2 ALRM TERM CONT STOP TSTP TTIN TTOU WINCH

// Service is one incarnation of the process service. Its operation records
// live in memory; a new Service has a new ServerInstanceID.
type Service struct {
	instance sandboxwire.ID
	caps     sp.Capabilities
	cfg      Config
	// stat reads a process's /proc stat; tests replace it.
	stat func(pid int) (procStat, error)
	// onExpire runs after an expired owner-loss grace has decided the
	// cleanup, before the operations are cancelled; tests restore there.
	onExpire func()
	// beforeRead runs before each output read; tests hold the readers there.
	beforeRead func()

	mu     sync.Mutex
	ops    map[opKey]*operation
	owners map[sandboxwire.ID]*time.Timer
	// stale holds attachments whose operations were cleaned up; they start
	// nothing more until ownership is restored.
	stale  map[sandboxwire.ID]bool
	active atomic.Int64
}

var _ sp.Service = (*Service)(nil)

// ErrPidfdUnsupported reports a kernel without pidfd_open or
// pidfd_send_signal, which Linux 5.3 and later have. Signals, Cancel and
// ownership cleanup depend on them, so the service does not start without.
var ErrPidfdUnsupported = errors.New("processservice: pidfd_open and pidfd_send_signal are required (Linux 5.3 or later)")

// New returns a service with a fresh incarnation. It fails with
// ErrPidfdUnsupported when the kernel lacks pidfds.
func New(cfg Config) (*Service, error) {
	caps := sp.Capabilities{
		Platform:                   sp.PlatformLinux,
		Scopes:                     []sp.Scope{sp.ScopePOSIXSession},
		IOModes:                    []sp.IOMode{sp.IOPipes, sp.IOPTY},
		Signals:                    signals,
		SignalTargets:              []sp.SignalTarget{sp.TargetLeader, sp.TargetInitialProcessGroup, sp.TargetPTYForegroundGroup, sp.TargetScope},
		PTYModes:                   supportedModes(),
		MaxStartBytes:              sandboxwire.MaxPayload,
		MaxDataBytes:               sandboxwire.MaxChunk,
		MaxActiveOperations:        uint32(cfg.MaxActiveOperations),
		MaxOperationRecords:        uint32(cfg.MaxOperationRecords),
		MaxReplayBytesPerOperation: uint32(cfg.MaxReplayBytesPerOperation),
		OwnerLossGraceMillis:       uint32(cfg.OwnerLossGrace.Milliseconds()),
		CancelGraceLimitMillis:     uint32(cfg.CancelGraceLimit.Milliseconds()),
	}
	if cfg.MaxActiveOperations <= 0 || cfg.MaxOperationRecords < cfg.MaxActiveOperations || cfg.OwnerLossGrace <= 0 || cfg.CancelGraceLimit < 0 {
		return nil, errors.New("processservice: invalid limits")
	}
	if err := caps.Validate(); err != nil {
		return nil, err
	}
	if err := probePidfd(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPidfdUnsupported, err)
	}
	return &Service{
		instance: sandboxwire.NewID(), caps: caps, cfg: cfg, stat: readStat,
		ops: map[opKey]*operation{}, owners: map[sandboxwire.ID]*time.Timer{}, stale: map[sandboxwire.ID]bool{},
	}, nil
}

// probePidfd opens a pidfd for the service's own process and sends it the
// null signal.
func probePidfd() error {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return fmt.Errorf("pidfd_open: %w", err)
	}
	defer unix.Close(fd)
	if err := unix.PidfdSendSignal(fd, 0, nil, 0); err != nil {
		return fmt.Errorf("pidfd_send_signal: %w", err)
	}
	return nil
}

func (s *Service) Describe(context.Context, *sp.Conn, sp.DescribeRequest) (sp.DescribeResponse, error) {
	return sp.DescribeResponse{ServerInstanceID: s.instance, Capabilities: s.caps}, nil
}

func (s *Service) checkInstance(ref sp.OperationRef) error {
	if ref.ServerInstanceID != s.instance {
		return sp.Fail(sp.CodeInstanceChanged, sandboxwire.EffectNone, "the service instance is %s", s.instance)
	}
	return nil
}

func (s *Service) lookup(conn *sp.Conn, ref sp.OperationRef) (*operation, error) {
	if err := s.checkInstance(ref); err != nil {
		return nil, err
	}
	s.mu.Lock()
	op := s.ops[opKey{conn.Attachment().ID, ref.OperationID}]
	s.mu.Unlock()
	if op == nil {
		return nil, sp.Fail(sp.CodeNotFound, sandboxwire.EffectNone, "no operation %s", ref.OperationID)
	}
	return op, nil
}

// Start reserves the ID, then launches once. The record is kept for the
// incarnation, so an ID is never launched twice. An attachment whose
// operations were cleaned up still finds its existing operations but launches
// nothing new until its ownership is restored.
func (s *Service) Start(_ context.Context, conn *sp.Conn, req sp.StartRequest) (sp.StartResponse, error) {
	if err := s.checkInstance(req.OperationRef); err != nil {
		return sp.StartResponse{}, err
	}
	key := opKey{conn.Attachment().ID, req.OperationID}
	digest := req.Spec.Digest()
	s.mu.Lock()
	if op := s.ops[key]; op != nil {
		s.mu.Unlock()
		if op.digest != digest {
			return sp.StartResponse{}, sp.Fail(sp.CodeOperationConflict, sandboxwire.EffectNone, "operation %s has a different spec", req.OperationID)
		}
		if op.inspect().Released {
			return sp.StartResponse{}, released()
		}
		return sp.StartResponse{Disposition: sp.StartExisting}, nil
	}
	if s.stale[key.attachment] {
		s.mu.Unlock()
		return sp.StartResponse{}, sp.Fail(sp.CodeStaleAttachment, sandboxwire.EffectNone, "the attachment's ownership ended")
	}
	if f := s.caps.CheckStart(req.Spec); f != nil {
		s.mu.Unlock()
		return sp.StartResponse{}, f
	}
	if len(s.ops) >= s.cfg.MaxOperationRecords || s.active.Load() >= int64(s.cfg.MaxActiveOperations) {
		s.mu.Unlock()
		return sp.StartResponse{}, sp.Fail(sp.CodeResourceExhausted, sandboxwire.EffectNone, "operation capacity is exhausted")
	}
	op := newOperation(s, key, digest)
	s.ops[key] = op
	s.active.Add(1)
	op.mu.Lock()
	op.observeLocked(conn, 0)
	op.mu.Unlock()
	s.mu.Unlock()
	op.launch(req.Spec)
	return sp.StartResponse{Disposition: sp.StartCreated}, nil
}

func (s *Service) Attach(_ context.Context, conn *sp.Conn, req sp.AttachRequest) (sp.AttachResponse, error) {
	op, err := s.lookup(conn, req.OperationRef)
	if err != nil {
		return sp.AttachResponse{}, err
	}
	st, err := op.attach(conn, req.AfterSequence)
	return sp.AttachResponse{Status: st}, err
}

func (s *Service) Inspect(_ context.Context, conn *sp.Conn, req sp.InspectRequest) (sp.InspectResponse, error) {
	op, err := s.lookup(conn, req.OperationRef)
	if err != nil {
		return sp.InspectResponse{}, err
	}
	return sp.InspectResponse{Status: op.inspect()}, nil
}

func (s *Service) WriteStdin(ctx context.Context, conn *sp.Conn, req sp.WriteStdinRequest) (sp.WriteStdinResponse, error) {
	op, err := s.lookup(conn, req.OperationRef)
	if err != nil {
		return sp.WriteStdinResponse{}, err
	}
	n, err := op.writeStdin(ctx, req.Offset, req.Data)
	return sp.WriteStdinResponse{Accepted: n}, err
}

func (s *Service) CloseStdin(_ context.Context, conn *sp.Conn, req sp.CloseStdinRequest) (sp.CloseStdinResponse, error) {
	op, err := s.lookup(conn, req.OperationRef)
	if err != nil {
		return sp.CloseStdinResponse{}, err
	}
	return sp.CloseStdinResponse{}, op.closeStdin(req.Offset)
}

func (s *Service) CloseOutput(_ context.Context, conn *sp.Conn, req sp.CloseOutputRequest) (sp.CloseOutputResponse, error) {
	op, err := s.lookup(conn, req.OperationRef)
	if err != nil {
		return sp.CloseOutputResponse{}, err
	}
	return sp.CloseOutputResponse{}, op.closeOutput(req.Stream)
}

func (s *Service) ResizePTY(_ context.Context, conn *sp.Conn, req sp.ResizePTYRequest) (sp.ResizePTYResponse, error) {
	op, err := s.lookup(conn, req.OperationRef)
	if err != nil {
		return sp.ResizePTYResponse{}, err
	}
	return sp.ResizePTYResponse{}, op.resize(req.Size)
}

func (s *Service) Signal(_ context.Context, conn *sp.Conn, req sp.SignalRequest) (sp.SignalResponse, error) {
	if f := s.caps.CheckSignal(req.Signal, req.Target); f != nil {
		return sp.SignalResponse{}, f
	}
	op, err := s.lookup(conn, req.OperationRef)
	if err != nil {
		return sp.SignalResponse{}, err
	}
	return sp.SignalResponse{}, op.signal(req.Signal, req.Target)
}

// Cancel caps the grace at the advertised limit.
func (s *Service) Cancel(_ context.Context, conn *sp.Conn, req sp.CancelRequest) (sp.CancelResponse, error) {
	op, err := s.lookup(conn, req.OperationRef)
	if err != nil {
		return sp.CancelResponse{}, err
	}
	grace := time.Duration(min(req.GraceMillis, s.caps.CancelGraceLimitMillis)) * time.Millisecond
	return sp.CancelResponse{}, op.cancel(grace)
}

func (s *Service) AckEvents(_ context.Context, conn *sp.Conn, req sp.AckEventsRequest) (sp.AckEventsResponse, error) {
	op, err := s.lookup(conn, req.OperationRef)
	if err != nil {
		return sp.AckEventsResponse{}, err
	}
	return sp.AckEventsResponse{}, op.ack(req.Sequence)
}

func (s *Service) Release(_ context.Context, conn *sp.Conn, req sp.ReleaseRequest) (sp.ReleaseResponse, error) {
	op, err := s.lookup(conn, req.OperationRef)
	if err != nil {
		return sp.ReleaseResponse{}, err
	}
	return sp.ReleaseResponse{}, op.release()
}

// AttachmentLost starts the owner-loss grace for an attachment whose Link
// ownership lapsed. Losing a stream alone does not call it.
func (s *Service) AttachmentLost(id sandboxwire.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[id] != nil {
		return
	}
	var t *time.Timer
	t = time.AfterFunc(s.cfg.OwnerLossGrace, func() {
		// The grace is current, the attachment is marked stale and its
		// operations are collected in one critical section, so a restore
		// either stops this grace first or comes after the cleanup.
		s.mu.Lock()
		var ops []*operation
		if s.owners[id] == t {
			delete(s.owners, id)
			ops = s.staleLocked(id)
		}
		s.mu.Unlock()
		if s.onExpire != nil {
			s.onExpire()
		}
		s.cancelAll(ops)
	})
	s.owners[id] = t
}

// AttachmentRestored ends the grace: the attachment's operations continue,
// and it can start operations again.
func (s *Service) AttachmentRestored(id sandboxwire.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopGraceLocked(id)
	delete(s.stale, id)
}

// AttachmentRevoked cleans up an attachment's operations at once.
func (s *Service) AttachmentRevoked(id sandboxwire.ID) {
	s.mu.Lock()
	s.stopGraceLocked(id)
	ops := s.staleLocked(id)
	s.mu.Unlock()
	s.cancelAll(ops)
}

// Shutdown ends the incarnation's operations when the service stops: it
// cancels every operation as ownership cleanup does, with TERM, then KILL
// after the grace limit, and returns once every operation's scope has closed
// or ctx ends. The binary calls it after its streams have ended, so no Start
// arrives during or after it; Reap must still be running.
func (s *Service) Shutdown(ctx context.Context) {
	s.mu.Lock()
	ops := make([]*operation, 0, len(s.ops))
	for _, op := range s.ops {
		ops = append(ops, op)
	}
	s.mu.Unlock()
	s.cancelAll(ops)
	for _, op := range ops {
		op.awaitScope(ctx)
	}
}

func (s *Service) stopGraceLocked(id sandboxwire.ID) {
	if t := s.owners[id]; t != nil {
		t.Stop()
		delete(s.owners, id)
	}
}

// staleLocked marks the attachment stale and returns its operations for
// cleanup. Doing both in one critical section means every operation it
// started is either returned or never launched; one still starting is
// cancelled when its launch completes.
func (s *Service) staleLocked(id sandboxwire.ID) []*operation {
	s.stale[id] = true
	var ops []*operation
	for key, op := range s.ops {
		if key.attachment == id {
			ops = append(ops, op)
		}
	}
	return ops
}

// cancelAll cancels the operations with the grace limit: TERM, then KILL.
func (s *Service) cancelAll(ops []*operation) {
	for _, op := range ops {
		op.cancel(s.cfg.CancelGraceLimit)
	}
}
