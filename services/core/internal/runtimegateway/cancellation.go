package runtimegateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

// ArchivedCancellationStore is deliberately separate from RuntimeStore: a
// receipt opportunity cannot authenticate a new connection or authorize work.
type ArchivedCancellationStore interface {
	ArchivedCancellationReceipt(context.Context, string, string, []string) (runtimedevice.ArchivedCancellationReceipt, error)
}

// TrackExecutionDelivery retains the exact existing Core delivery through its
// terminal database commit. Durable subscriptions may already have seen Done or
// been removed while cancellation ACK/commit is still pending.
func (s *Session) TrackExecutionDelivery(runID string) (func(), error) {
	s.receiptMu.Lock()
	defer s.receiptMu.Unlock()
	if runID == "" || s.IsClosed() || s.receiptDrain.RunID != "" {
		return nil, ErrSessionClosed
	}
	if s.deliveries == nil {
		s.deliveries = make(map[string]struct{})
	}
	if _, exists := s.deliveries[runID]; exists {
		return nil, errors.New("execution delivery already tracked")
	}
	s.deliveries[runID] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.receiptMu.Lock()
			delete(s.deliveries, runID)
			closePeer := s.receiptDrain.RunID == runID
			s.receiptMu.Unlock()
			if closePeer {
				s.CloseWithCode(CloseRuntimeDeleted, "archived cancellation settled")
			}
		})
	}, nil
}

// DrainArchivedCancellation checks both durable archive identity and this exact
// connection's unfinished delivery. It performs no liveness or credential write.
func (s *Session) DrainArchivedCancellation(ctx context.Context) (bool, error) {
	if s.archivedCancellations == nil || s.IsClosed() {
		return false, nil
	}
	s.receiptMu.Lock()
	ids := make([]string, 0, len(s.deliveries))
	for id := range s.deliveries {
		ids = append(ids, id)
	}
	s.receiptMu.Unlock()
	if len(ids) == 0 {
		return false, nil
	}
	receipt, err := s.archivedCancellations.ArchivedCancellationReceipt(ctx, s.DeviceID, s.credentialHash, ids)
	if err != nil || receipt.RunID == "" {
		return false, err
	}
	s.receiptMu.Lock()
	defer s.receiptMu.Unlock()
	if _, exists := s.deliveries[receipt.RunID]; !exists || s.IsClosed() || !time.Now().Before(receipt.Deadline) {
		return false, nil
	}
	if s.receiptDrain.RunID != "" {
		// Once fenced, neither a later cancel nor a changed response extends it.
		return s.receiptDrain.RunID == receipt.RunID && s.receiptDrain.Deadline.Equal(receipt.Deadline), nil
	}
	s.receiptDrain = receipt
	s.receiptTimer = time.AfterFunc(time.Until(receipt.Deadline), func() { s.CloseWithCode(CloseRuntimeDeleted, "archived cancellation deadline") })
	return true, nil
}

func (s *Session) receiptDraining() bool {
	s.receiptMu.Lock()
	defer s.receiptMu.Unlock()
	return s.receiptDrain.RunID != ""
}

func (s *Session) allowsReceiptFrame(env proto.Envelope, outbound bool) bool {
	s.receiptMu.Lock()
	defer s.receiptMu.Unlock()
	if s.receiptDrain.RunID == "" {
		return true
	}
	if !time.Now().Before(s.receiptDrain.Deadline) {
		return false
	}
	if !outbound && env.Type == proto.TypeHeartbeat {
		return true
	}
	if env.ID != s.receiptDrain.RunID {
		return false
	}
	if outbound {
		return env.Type == proto.TypePromptCancel
	}
	if env.Type == proto.TypeInteractionDecisionAck {
		var ack proto.InteractionDecisionAckPayload
		return env.DecodePayload(&ack) == nil && ack.DeliveryID == "cancel:"+env.ID
	}
	// Existing run events must remain lossless, including child observations and
	// Done before ACK. Workspace/preparation replies have other correlation IDs.
	return true
}

func (s *Session) stopReceiptTimer() {
	s.receiptMu.Lock()
	defer s.receiptMu.Unlock()
	if s.receiptTimer != nil {
		s.receiptTimer.Stop()
	}
}
