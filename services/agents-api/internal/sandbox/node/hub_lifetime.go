package node

import (
	"context"
	"time"
)

const callbackTimeout = 5 * time.Second

// A reservation is retained until callbacks and fenced cleanup have returned.
// Callbacks must honor context cancellation and must not detach database writes.
type connectionLifetime struct {
	cancel context.CancelFunc
}

func (h *Hub) lifetime(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(h.ctx, cancel)
	if h.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

func callbackValue[T any](parent context.Context, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(parent, callbackTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	value, err := fn(ctx)
	if err == nil {
		err = ctx.Err()
	}
	return value, err
}

func callback(parent context.Context, fn func(context.Context) error) error {
	_, err := callbackValue(parent, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

func (p *peer) lockSend(ctx context.Context) error {
	select {
	case p.send <- struct{}{}:
		return nil
	case <-p.done:
		return ErrUnavailable
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *peer) unlockSend() { <-p.send }
