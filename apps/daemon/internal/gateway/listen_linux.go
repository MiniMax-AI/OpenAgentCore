//go:build linux

package gateway

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

// listen opens a TCP listener on 127.0.0.1 at each port inside the network
// namespace ns. A socket stays in the namespace it was created in, so the
// listeners are opened on a thread that joined ns and then served from the
// daemon's own threads.
func listen(ns *os.File, ports []int) ([]net.Listener, error) {
	type result struct {
		lns []net.Listener
		err error
	}
	done := make(chan result, 1)
	go func() {
		// Never unlocked: the thread exits with the goroutine instead of
		// returning to the scheduler inside the Session's namespace.
		runtime.LockOSThread()
		if err := unix.Setns(int(ns.Fd()), unix.CLONE_NEWNET); err != nil {
			done <- result{err: fmt.Errorf("%w: join: %w", ErrNetwork, err)}
			return
		}
		var lns []net.Listener
		for _, port := range ports {
			ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err != nil {
				for _, l := range lns {
					l.Close()
				}
				done <- result{err: fmt.Errorf("%w: listen: %w", ErrNetwork, err)}
				return
			}
			lns = append(lns, ln)
		}
		done <- result{lns: lns}
	}()
	r := <-done
	return r.lns, r.err
}
