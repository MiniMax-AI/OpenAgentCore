//go:build linux

package processbroker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// stream is one Process stream and the service incarnation behind it.
type stream struct {
	client   *sp.Client
	instance sandboxwire.ID
	caps     sp.Capabilities
}

// ended reports whether the stream is over; a request on it fails.
func (s *stream) ended() bool { return s.client.Err() != nil }

// link keeps one Process stream for the Session and redials it when it ends.
type link struct {
	dial func(context.Context) (io.ReadWriteCloser, error)
	log  *slog.Logger

	// dialing admits one redial at a time; the others wait for its stream.
	dialing chan struct{}

	mu  sync.Mutex
	cur *stream
}

const (
	minBackoff = 50 * time.Millisecond
	maxBackoff = 5 * time.Second
)

func newLink(dial func(context.Context) (io.ReadWriteCloser, error), log *slog.Logger) *link {
	return &link{dial: dial, log: log, dialing: make(chan struct{}, 1)}
}

// get returns the live stream, redialing until one is up or ctx ends.
func (l *link) get(ctx context.Context) (*stream, error) {
	if s := l.current(); s != nil {
		return s, nil
	}
	select {
	case l.dialing <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-l.dialing }()
	if s := l.current(); s != nil {
		return s, nil
	}
	backoff := minBackoff
	for {
		s, err := l.connect(ctx)
		if err == nil {
			l.mu.Lock()
			l.cur = s
			l.mu.Unlock()
			return s, nil
		}
		l.log.Warn("process stream unavailable", "error", err, "retry", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		backoff = min(2*backoff, maxBackoff)
	}
}

func (l *link) current() *stream {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur != nil && !l.cur.ended() {
		return l.cur
	}
	return nil
}

// connect dials and describes the service, which pins the incarnation the
// stream's operations belong to.
func (l *link) connect(ctx context.Context) (*stream, error) {
	rw, err := l.dial(ctx)
	if err != nil {
		return nil, err
	}
	c := sp.NewClient(rw)
	d, err := c.Describe(ctx)
	if err != nil {
		c.Close()
		return nil, err
	}
	return &stream{client: c, instance: d.ServerInstanceID, caps: d.Capabilities}, nil
}

func (l *link) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur != nil {
		l.cur.client.Close()
	}
}
