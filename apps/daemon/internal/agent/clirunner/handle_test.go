package clirunner

import (
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestHandleProcessCancellation(t *testing.T) {
	const grace = 100 * time.Millisecond
	for name, termExit := range map[string]int{"ignores TERM": -1, "exits 3 on TERM": 3} {
		t.Run(name, func(t *testing.T) {
			h := &fakeHandle{termExit: termExit, exit: make(chan int, 1), closed: make(chan struct{})}
			p, err := FromHandle(h, HandleOptions{Stdout: emptyReader(), Stderr: emptyReader(), KillTimeout: grace})
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			p.Cancel()
			if signals := h.received(); !slices.Equal(signals, []syscall.Signal{syscall.SIGTERM}) {
				t.Fatalf("signals = %v, want TERM", signals)
			}
			<-p.Done()
			waitErr := p.Wait()
			code, ok := p.ExitCode()
			if termExit < 0 {
				if h.closedAt.Sub(started) < grace || waitErr == nil || ok {
					t.Fatalf("closed after %v, Wait = %v, ExitCode ok = %v; want close after the grace and an unknown exit", h.closedAt.Sub(started), waitErr, ok)
				}
				return
			}
			if waitErr == nil || !ok || code != termExit {
				t.Fatalf("Wait = %v, ExitCode = %d, %v; want exit %d", waitErr, code, ok, termExit)
			}
			select {
			case <-h.closed:
			default:
				t.Fatal("Wait did not close the handle")
			}
		})
	}
}

type fakeHandle struct {
	termExit  int
	exit      chan int
	closed    chan struct{}
	closeOnce sync.Once
	closedAt  time.Time
	mu        sync.Mutex
	signals   []syscall.Signal
}

func (h *fakeHandle) Signal(sig syscall.Signal) error {
	h.mu.Lock()
	h.signals = append(h.signals, sig)
	h.mu.Unlock()
	if sig == syscall.SIGTERM && h.termExit >= 0 {
		h.exit <- h.termExit
	}
	return nil
}

func (h *fakeHandle) Wait() (int, error) {
	select {
	case code := <-h.exit:
		return code, nil
	case <-h.closed:
		return 0, errors.New("closed")
	}
}

func (h *fakeHandle) Close() error {
	h.closeOnce.Do(func() {
		h.closedAt = time.Now()
		close(h.closed)
	})
	return nil
}

func (h *fakeHandle) received() []syscall.Signal {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.signals)
}

func emptyReader() io.ReadCloser { return io.NopCloser(strings.NewReader("")) }
