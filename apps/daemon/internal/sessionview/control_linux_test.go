//go:build linux

package sessionview

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestSendIsBounded checks that a send waiting for room on the socket or for another send returns once its context ends, without disturbing the send in progress, and that a shutdown ends the send in progress.
func TestSendIsBounded(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	peer := os.NewFile(uintptr(pair[1]), "peer")
	defer peer.Close()
	c, err := newControl(os.NewFile(uintptr(pair[0]), "control"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	big := message{Targets: map[string]string{"/": strings.Repeat("x", 32<<10)}}
	bounded := func(d time.Duration) error {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		return c.send(ctx, big)
	}
	// The peer reads nothing, so the socket fills.
	for {
		start := time.Now()
		err := bounded(20 * time.Millisecond)
		if time.Since(start) > 2*time.Second {
			t.Fatalf("send took %v with a 20ms bound", time.Since(start))
		}
		if errors.Is(err, context.DeadlineExceeded) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	blocked := make(chan error, 1)
	go func() { blocked <- c.send(context.Background(), big) }()
	for len(c.sending) == 0 {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	if err := bounded(20 * time.Millisecond); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Errorf("send behind a blocked send = %v after %v, want context.DeadlineExceeded within 20ms", err, time.Since(start))
	}
	select {
	case err := <-blocked:
		t.Fatalf("unbounded send returned %v with the socket full", err)
	case <-time.After(50 * time.Millisecond):
	}
	c.interrupt()
	select {
	case err := <-blocked:
		if err == nil {
			t.Error("send after the shutdown succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("send still blocked 2s after the shutdown")
	}
}
