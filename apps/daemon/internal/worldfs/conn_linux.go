//go:build linux

package worldfs

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

var (
	errDead        = errors.New("worldfs: the world is lost")
	errInterrupted = errors.New("worldfs: interrupted before the request was sent")
	errStopped     = errors.New("worldfs: the world stopped")
)

// connect opens a stream and describes the service within ctx.
func (f *frontend) connect(ctx context.Context) (*sandboxfs.Client, *sandboxfs.DescribeResponse, error) {
	rw, err := f.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	c := sandboxfs.NewClient(rw)
	d, err := c.Describe(ctx, &sandboxfs.DescribeRequest{})
	if err != nil {
		c.Close()
		return nil, nil, err
	}
	return c, d, nil
}

// client returns the stream's client. After the stream failed it redials within ctx and continues only with the same service instance. Once shutdown began it returns no stream. A failed redial is an [ErrConnect] error, and an interrupt while waiting for the stream or redialing is errInterrupted: either way the request was never sent.
func (f *frontend) client(ctx context.Context, interrupt <-chan struct{}) (*sandboxfs.Client, error) {
	select {
	case f.connTurn <- struct{}{}:
	case <-ctx.Done():
		return nil, &Error{Kind: ErrConnect, Op: "reconnect", Err: ctx.Err()}
	case <-interrupt:
		return nil, errInterrupted
	}
	defer func() { <-f.connTurn }()
	if f.dead.Load() {
		return nil, errDead
	}
	if f.ctx.Err() != nil {
		return nil, &Error{Kind: ErrConnect, Op: "reconnect", Err: errStopped}
	}
	if c := f.conn; c != nil {
		select {
		case <-c.Done():
		default:
			return c, nil
		}
	}
	dial, cancel := context.WithCancel(ctx)
	defer cancel()
	if interrupt != nil {
		go func() {
			select {
			case <-interrupt:
				cancel()
			case <-dial.Done():
			}
		}()
	}
	c, d, err := f.connect(dial)
	switch {
	case err != nil && interrupted(interrupt):
		return nil, errInterrupted
	case err != nil:
		f.observe(err)
		return nil, &Error{Kind: ErrConnect, Op: "reconnect", Err: err}
	case d.ServerInstanceID != f.instance:
		c.Close()
		f.lose(&Error{Kind: ErrInstanceChanged, Op: "reconnect"}, true)
		return nil, errDead
	}
	f.conn = c
	f.gen.Add(1)
	f.wake() // what the failed stream never sent goes on the new one
	return c, nil
}

// call sends one request. A request the stream failed before sending cannot have taken effect, so it is sent once more on a new stream; nothing else is retried.
func call[Q, R any](f *frontend, ctx context.Context, op func(*sandboxfs.Client, context.Context, Q) (R, error), q Q) (R, error) {
	return callUntil(f, ctx, nil, op, q)
}

// callUntil is call for a request the kernel may interrupt. An interrupt before the request is sent abandons it with errInterrupted; after that the call waits for the request's own outcome (see [sandboxfs.WithInterrupt]).
func callUntil[Q, R any](f *frontend, ctx context.Context, interrupt <-chan struct{}, op func(*sandboxfs.Client, context.Context, Q) (R, error), q Q) (R, error) {
	var r R
	var err error
	sent := ctx
	if interrupt != nil {
		sent = sandboxfs.WithInterrupt(ctx, interrupt)
	}
	for range 2 {
		var c *sandboxfs.Client
		if c, err = f.client(ctx, interrupt); err != nil {
			return r, err
		}
		if interrupted(interrupt) {
			return r, errInterrupted
		}
		if r, err = op(c, sent, q); err == nil {
			return r, nil
		}
		f.observe(err)
		if !unsent(err) {
			break
		}
	}
	return r, err
}

func interrupted(interrupt <-chan struct{}) bool {
	select {
	case <-interrupt:
		return true
	default:
		return false
	}
}

// unsent reports whether err shows that the request never left the frontend: the redial failed, or the stream had failed before the request was written.
func unsent(err error) bool {
	var fail *sandboxfs.Failure
	return errors.Is(err, ErrConnect) || errors.As(err, &fail) && fail.Effect == sandboxwire.EffectNone && errors.Is(err, sandboxfs.ErrTransport)
}

// retryable reports whether a request certainly did nothing and may succeed when sent again: it was never sent, or the service refused it for now with a retryable code and EffectNone.
func retryable(err error) bool {
	var fail *sandboxfs.Failure
	return unsent(err) || errors.As(err, &fail) && fail.Code.Retryable() && fail.Effect == sandboxwire.EffectNone
}

// noEffect reports whether a request certainly changed nothing.
func noEffect(err error) bool {
	var fail *sandboxfs.Failure
	return unsent(err) || errors.Is(err, errDead) || errors.Is(err, errInterrupted) || errors.As(err, &fail) && fail.Effect == sandboxwire.EffectNone
}
