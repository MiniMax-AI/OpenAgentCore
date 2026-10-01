//go:build linux

package processbroker

import (
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Two invocations writing to one pipe race for the space a reader frees; the
// loser must still stop when aborted, and the shared description must keep
// its flags.
func TestWritersSharingAFullPipeStop(t *testing.T) {
	var p [2]int
	if err := unix.Pipe2(p[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(p[0])
	defer unix.Close(p[1])
	done := make(chan error, 2)
	var stops []*stopFlag
	for range 2 {
		fd, err := unix.FcntlInt(uintptr(p[1]), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		ep, err := openEndpoint(fd, true, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ep.close()
		stop, err := newStopFlag()
		if err != nil {
			t.Fatal(err)
		}
		defer stop.close()
		stops = append(stops, stop)
		go func() { done <- writeFD(ep, make([]byte, 1<<20), stop) }()
	}
	// Free space now and then so both writers wake for it, then stop reading.
	buf := make([]byte, pipeBuf)
	for range 20 {
		time.Sleep(10 * time.Millisecond)
		if _, err := unix.Read(p[0], buf); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range stops {
		s.set()
	}
	for range 2 {
		select {
		case err := <-done:
			if err != errStopped {
				t.Fatalf("writeFD = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a writer did not stop")
		}
	}
	if fl, err := unix.FcntlInt(uintptr(p[1]), unix.F_GETFL, 0); err != nil || fl&unix.O_NONBLOCK != 0 {
		t.Fatalf("shared flags %#x, %v", fl, err)
	}
}
