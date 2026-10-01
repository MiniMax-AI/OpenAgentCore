package clirunner

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestHandleProcessCancellation(t *testing.T) {
	const grace = 100 * time.Millisecond
	for name, termExit := range map[string]int{"ignores TERM": -1, "exits 0 on TERM": 0, "exits 3 on TERM": 3} {
		t.Run(name, func(t *testing.T) {
			h := newFakeHandle(termExit)
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
			switch {
			case termExit < 0:
				if h.closedAt.Sub(started) < grace || waitErr == nil || ok {
					t.Fatalf("closed after %v, Wait = %v, ExitCode ok = %v; want close after the grace and an unknown exit", h.closedAt.Sub(started), waitErr, ok)
				}
			case termExit == 0:
				if !errors.Is(waitErr, context.Canceled) || !ok || code != 0 {
					t.Fatalf("Wait = %v, ExitCode = %d, %v; want the context error and exit 0", waitErr, code, ok)
				}
			default:
				if waitErr == nil || errors.Is(waitErr, context.Canceled) || !ok || code != termExit {
					t.Fatalf("Wait = %v, ExitCode = %d, %v; want exit %d", waitErr, code, ok, termExit)
				}
			}
			if !h.isClosed() {
				t.Fatal("Wait did not close the handle")
			}
		})
	}
}

func TestHandleProcessCancelAfterExit(t *testing.T) {
	h := newFakeHandle(-1)
	stdin, stdout, stderr := &closer{}, &closer{Reader: strings.NewReader("output")}, &closer{Reader: strings.NewReader("")}
	p, err := FromHandle(h, HandleOptions{Stdin: stdin, Stdout: stdout, Stderr: stderr})
	if err != nil {
		t.Fatal(err)
	}
	h.exit <- 0
	<-p.Done()
	p.Cancel()
	if signals := h.received(); len(signals) != 0 || h.isClosed() || stdout.isClosed() {
		t.Fatalf("Cancel after exit sent %v and closed the handle %v, stdout %v; want nothing", signals, h.isClosed(), stdout.isClosed())
	}
	if out, err := io.ReadAll(p.Stdout); err != nil || string(out) != "output" {
		t.Fatalf("read %q, %v; want the whole output", out, err)
	}
	for range 2 {
		if err := p.Wait(); err != nil {
			t.Fatalf("Wait = %v, want success", err)
		}
	}
	if !stdin.isClosed() || !stdout.isClosed() || !stderr.isClosed() || !h.isClosed() {
		t.Fatal("Wait did not close stdin, stdout, stderr and the handle")
	}
}

// TestHandleProcessCancelAfterLeaderExit checks that a cancel that finds the process exited 0 while its descendants still end leaves the success.
func TestHandleProcessCancelAfterLeaderExit(t *testing.T) {
	h := newFakeHandle(-1)
	h.leaderExited = true
	p, err := FromHandle(h, HandleOptions{Stdout: emptyReader(), Stderr: emptyReader()})
	if err != nil {
		t.Fatal(err)
	}
	p.Cancel()
	h.exit <- 0
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait = %v, want success", err)
	}
}

type fakeHandle struct {
	termExit     int
	leaderExited bool
	exit         chan int
	closed       chan struct{}
	closeOnce    sync.Once
	closedAt     time.Time
	mu           sync.Mutex
	signals      []syscall.Signal
}

func newFakeHandle(termExit int) *fakeHandle {
	return &fakeHandle{termExit: termExit, exit: make(chan int, 1), closed: make(chan struct{})}
}

func (h *fakeHandle) Signal(sig syscall.Signal) error {
	if h.leaderExited {
		return os.ErrProcessDone
	}
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

func (h *fakeHandle) isClosed() bool {
	select {
	case <-h.closed:
		return true
	default:
		return false
	}
}

func (h *fakeHandle) received() []syscall.Signal {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.signals)
}

// closer is a stdio end that records Close.
type closer struct {
	io.Reader
	mu     sync.Mutex
	closed bool
}

func (c *closer) Write(b []byte) (int, error) { return len(b), nil }

func (c *closer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *closer) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func emptyReader() io.ReadCloser { return io.NopCloser(strings.NewReader("")) }
