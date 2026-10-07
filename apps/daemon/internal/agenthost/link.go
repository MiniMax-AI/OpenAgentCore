package agenthost

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// dialFunc connects an attach link; onClosed is its OnAttachmentClosed.
type dialFunc func(ctx context.Context, onClosed func(sandboxlink.AttachmentClosed)) (*sandboxlink.AttachLink, error)

func relayDial(cfg Config) dialFunc {
	return func(ctx context.Context, onClosed func(sandboxlink.AttachmentClosed)) (*sandboxlink.AttachLink, error) {
		return sandboxlink.DialAttach(ctx, sandboxlink.AttachConfig{URL: cfg.RelayURL, TLS: cfg.TLS, RuntimeID: cfg.RuntimeID,
			Credential: cfg.Credential, OnAttachmentClosed: onClosed})
	}
}

const (
	// retryWait is the pause between attempts of a renewal or close that
	// failed with a retryable error.
	retryWait = 250 * time.Millisecond
	// closeBound bounds CloseAttachment at teardown, redials included.
	closeBound = 10 * time.Second
)

// linkOwner owns an Executor's Link attachment. It dials the relay when a
// stream is first needed and again after the link drops, pins the service
// instance the first Opened reports, renews the lease before it passes, and
// fails the Session on any Link failure that is not retryable and on the
// relay closing the attachment.
type linkOwner struct {
	dial    dialFunc
	binding Binding
	// attachment is the attachment's ID. An attachment that has closed is
	// never reopened, so each Executor opens its own.
	attachment sandboxwire.ID
	fail       func(error)

	dialMu sync.Mutex // serializes dials
	mu     sync.Mutex
	link   *sandboxlink.AttachLink
	// instance is the service instance of the first Opened; zero before.
	instance sandboxwire.ID
	lease    time.Time
	// opened records that an Open was sent, so the attachment may exist.
	opened  bool
	closing bool
	renewer chan struct{} // closed when the renewal loop returns; nil before it starts
	stop    context.CancelFunc
}

func newLinkOwner(dial dialFunc, b Binding, attachment sandboxwire.ID, fail func(error)) *linkOwner {
	return &linkOwner{dial: dial, binding: b, attachment: attachment, fail: fail}
}

// request is the Open of service on the attachment.
func (l *linkOwner) request(service sandboxlink.Service, version uint16, expected sandboxwire.ID) sandboxlink.Open {
	b := l.binding
	return sandboxlink.Open{Service: service, Version: version, Resource: b.Resource, ExpectedServerInstanceID: expected,
		AttachmentID: l.attachment, SessionID: b.SessionID, AssignmentID: b.AssignmentID, AssignmentEpoch: b.AssignmentEpoch,
		AttachGrant: b.AttachGrant}
}

// current returns the live link, dialing a new one when there is none.
func (l *linkOwner) current(ctx context.Context) (*sandboxlink.AttachLink, error) {
	l.dialMu.Lock()
	defer l.dialMu.Unlock()
	l.mu.Lock()
	link := l.link
	l.mu.Unlock()
	if link != nil {
		select {
		case <-link.Done():
			link.Close()
		default:
			return link, nil
		}
	}
	link, err := l.dial(ctx, l.closed)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.link = link
	l.mu.Unlock()
	return link, nil
}

// open opens a stream of service on the attachment.
func (l *linkOwner) open(ctx context.Context, service sandboxlink.Service, version uint16) (sandboxlink.Stream, error) {
	l.mu.Lock()
	closing := l.closing
	l.mu.Unlock()
	if closing {
		return nil, fmt.Errorf("%w: open %s: the Executor is closing", ErrLink, service)
	}
	link, err := l.current(ctx)
	if err != nil {
		return nil, l.observe("dial", err)
	}
	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		return nil, fmt.Errorf("%w: open %s: the Executor is closing", ErrLink, service)
	}
	l.opened = true
	expected := l.instance
	l.mu.Unlock()
	st, opened, err := link.OpenService(ctx, l.request(service, version, expected))
	if err != nil {
		return nil, l.observe("open "+service.String(), err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.instance.IsZero() {
		l.instance = opened.ServerInstanceID
	}
	if opened.LeaseExpiresAt.After(l.lease) {
		l.lease = opened.LeaseExpiresAt
	}
	if l.renewer == nil && !l.closing {
		ctx, stop := context.WithCancel(context.Background())
		l.renewer, l.stop = make(chan struct{}), stop
		go l.renew(ctx)
	}
	return st, nil
}

// observe fails the Session on a Link failure that is not retryable and
// returns err as a typed error.
func (l *linkOwner) observe(op string, err error) error {
	err = fmt.Errorf("%w: %s: %w", ErrLink, op, err)
	if !retryable(err) {
		l.report(err)
	}
	return err
}

// report fails the Session with err unless close has begun: from then on the
// Executor's own close of the attachment explains whatever the Link reports.
func (l *linkOwner) report(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closing {
		l.fail(err)
	}
}

// retryable reports whether a failed Link request may succeed later: a Link
// failure whose code says so, or a transport failure.
func retryable(err error) bool {
	var le *sandboxlink.Error
	return !errors.As(err, &le) || le.Code.Retryable()
}

// closed is the link's OnAttachmentClosed. It never blocks.
func (l *linkOwner) closed(c sandboxlink.AttachmentClosed) {
	if c.AttachmentID == l.attachment {
		l.report(fmt.Errorf("%w: attachment: the relay closed the attachment (reason %d)", ErrLink, c.Reason))
	}
}

// renew extends the lease at half its remaining time until ctx ends. A
// renewal that fails retryably is tried again until the lease passes.
func (l *linkOwner) renew(ctx context.Context) {
	defer close(l.renewer)
	for {
		l.mu.Lock()
		lease := l.lease
		l.mu.Unlock()
		timer := time.NewTimer(time.Until(lease) / 2)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		for {
			if !time.Now().Before(lease) {
				l.report(fmt.Errorf("%w: renew: %w", ErrLink, sandboxlink.LeaseExpired))
				return
			}
			attempt, cancel := context.WithDeadline(ctx, lease)
			renewed, err := l.renewOnce(attempt)
			cancel()
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				l.mu.Lock()
				if renewed.LeaseExpiresAt.After(l.lease) {
					l.lease = renewed.LeaseExpiresAt
				}
				l.mu.Unlock()
				break
			}
			if err = l.observe("renew", err); !retryable(err) {
				return
			}
			if !sleep(ctx, retryWait) {
				return
			}
		}
	}
}

func (l *linkOwner) renewOnce(ctx context.Context) (sandboxlink.AttachmentRenewed, error) {
	link, err := l.current(ctx)
	if err != nil {
		return sandboxlink.AttachmentRenewed{}, err
	}
	return link.Renew(ctx, sandboxlink.RenewAttachment{AttachmentID: l.attachment, AttachGrant: l.binding.AttachGrant})
}

// close stops renewal, closes the attachment when an Open may have created
// it, and closes the link. Later opens fail, and later closes do nothing.
func (l *linkOwner) close() error {
	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		return nil
	}
	l.closing = true
	opened, renewer, stop := l.opened, l.renewer, l.stop
	l.mu.Unlock()
	if stop != nil {
		stop()
		<-renewer
	}
	var err error
	if opened {
		ctx, cancel := context.WithTimeout(context.Background(), closeBound)
		for {
			var link *sandboxlink.AttachLink
			if link, err = l.current(ctx); err == nil {
				err = link.CloseAttachment(ctx, l.attachment)
			}
			if err == nil || !retryable(err) || !sleep(ctx, retryWait) {
				break
			}
		}
		cancel()
	}
	l.mu.Lock()
	link := l.link
	l.link = nil
	l.mu.Unlock()
	if link != nil {
		link.Close()
	}
	if err != nil {
		return fmt.Errorf("%w: close attachment: %w", ErrTeardown, err)
	}
	return nil
}

// sleep waits d and reports whether ctx is still live.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
