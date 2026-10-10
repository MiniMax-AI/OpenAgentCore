package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Request sends a JSON-RPC call and blocks until the response arrives,
// the deadline fires, or the child exits. Returns the raw result JSON
// so the caller can pick its decode shape.
func (c *JSONRPCClient) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.request(ctx, method, params, c.writeFrame)
}

func (c *JSONRPCClient) request(ctx context.Context, method string, params any, write func(any) error) (json.RawMessage, error) {
	return c.requestWithTimeout(ctx, method, params, write, c.cfg.RequestTimeout, nil)
}

// The result hook runs on the read loop before any following notification.
func (c *JSONRPCClient) requestWithResult(ctx context.Context, method string, params any, onResult func(json.RawMessage) error) (json.RawMessage, error) {
	return c.requestWithTimeout(ctx, method, params, c.writeFrame, c.cfg.RequestTimeout, onResult)
}

func (c *JSONRPCClient) requestWithTimeout(ctx context.Context, method string, params any, write func(any) error, timeout time.Duration, onResult func(json.RawMessage) error) (json.RawMessage, error) {
	started := time.Now()
	if !c.Alive() {
		return nil, errors.New("codex rpc: client not alive")
	}
	id, err := newRequestID()
	if err != nil {
		return nil, fmt.Errorf("codex rpc: id: %w", err)
	}
	pending := &pendingRequest{
		method:   method,
		resp:     make(chan rpcResponse, 1),
		onResult: onResult,
	}
	if method == "initialize" {
		type responseObservation struct {
			at       time.Time
			accepted bool
		}
		observed := make(chan responseObservation, 1)
		pending.onResponse = func(success bool) {
			// Only capture on the reader: logging must not delay response delivery.
			observed <- responseObservation{at: time.Now(), accepted: success}
		}
		defer func() {
			select {
			case response := <-observed:
				var responseErr error
				if !response.accepted {
					responseErr = errors.New("native initialize rejection")
				}
				observePreparationInterval(ctx, "rpc_initialize_response", started, response.at, responseErr)
			default:
			}
		}()
	}
	c.pendingMu.Lock()
	c.pending[id] = pending
	c.pendingMu.Unlock()

	frame := JsonRpcRequest{JsonRpc: JsonRpcVersion, ID: id, Method: method, Params: params}
	err = write(frame)
	if method == "initialize" {
		ended, writeErr := time.Now(), err
		defer func() { observePreparationInterval(ctx, "rpc_initialize_write", started, ended, writeErr) }()
	}
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("codex rpc: write %s: %w", method, err)
	}

	var deadline <-chan time.Time
	if timeout > 0 {
		c.pendingMu.Lock()
		timer := time.NewTimer(timeout)
		if c.pending[id] == pending {
			pending.timer = timer
		} else {
			timer.Stop()
		}
		c.pendingMu.Unlock()
		defer timer.Stop()
		deadline = timer.C
	}

	select {
	case r := <-pending.resp:
		return r.result, r.err
	case <-deadline:
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("codex rpc: %s timed out after %s", method, timeout)
	case <-ctx.Done():
		// A durable response can arrive while its write-phase notification is sent.
		if timeout == 0 {
			select {
			case r := <-pending.resp:
				return r.result, r.err
			default:
			}
		}
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	}
}
