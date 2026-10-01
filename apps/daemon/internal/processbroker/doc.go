// Package processbroker runs a Session's shim invocations in its sandbox.
//
// A Harness in the Session view executes oac-process-shim
// (apps/daemon/internal/processshim), which hands its invocation and its
// descriptors 0, 1 and 2 to the Session's process relay. The relay is the
// same binary in relay mode, which sessionview starts in the view as the
// Session user, with no capabilities, before the Harness. It is the only
// process that holds a descriptor from the view: it does every read, write
// and terminal ioctl on them with the Session's own authority. It hands the
// broker the request and the terminal's mode over a socketpair that
// sessionview creates. The broker does all process protocol work: it
// resolves the invocation against the declared executable table and
// environment policy, starts the program with the process protocol
// (internal/sandboxprocess), sends the relay the program's output and asks
// it for stdin, forwards the signals the shim reports, and has the relay
// send the shim the remote exit. Neither the shim nor the relay holds
// credentials.
//
// The broker treats the relay as untrusted Session input. It reads the
// socketpair without a control buffer, so the kernel closes any descriptor
// sent with a message, checks every message against the IPC's types and
// limits, and stops serving at the first message that breaks the IPC. That,
// or the relay's loss, fails the Session: Done closes and Err wraps
// ErrRelayLost. The broker never restarts the relay and never replays output
// whose delivery is uncertain. Close never waits on the relay.
//
// The relay pumps each descriptor on its own, and its dispatch never waits
// on one, so a descriptor nobody reads holds back only its own stream. The
// broker sends the relay at most processshim.OutputWindow bytes of a stream
// that the relay has not reported written, and acknowledges output to the
// process service only after that report, so the service in turn holds back
// the program.
//
// The relay never changes the flags of a passed descriptor, whose open file
// description the Harness shares. It reopens a pipe, FIFO or pty slave
// through /proc/self/fd as its own non-blocking description, uses a socket
// with MSG_DONTWAIT and a regular file or block device as it is, and polls
// any other descriptor before each call, so it waits on a peer only in a
// poll that ending the invocation interrupts. Output on an AF_UNIX socket
// carries the relay's own credentials.
//
// Invocations on one terminal share its saved mode in the relay: the first
// saves it, each runs the terminal raw, and the last to finish restores it.
//
// The broker forwards the exit without waiting for output, and the relay
// answers the shim once the output the program wrote before exiting is
// written. Each output descriptor closes after its stream's last byte is
// written, so a remote background job that keeps its output open keeps the
// Harness's pipe open while the shim still exits when the leader does.
// After the leader exits, remote background processes stay with the Session
// and are not cancelled; a shim lost before the exit cancels the operation's
// scope.
//
// Qualification limits. These behaviors differ from a native child:
//   - Stop and continue job control is incomplete. The shim reports TSTP,
//     TTIN and TTOU to the remote process group but does not stop itself, so
//     the Harness never sees the job stop. SIGSTOP of the shim stops only the
//     shim.
//   - Stdin is read ahead. The relay reads the shared stdin as the service
//     accepts it, so bytes the program never consumes are still taken from a
//     stdin the Harness shares with later commands. Forwarding stops when the
//     leader exits; remote background readers then see end of file.
//   - On a terminal, stderr is merged into the terminal output, as the
//     remote PTY merges it.
//   - A descriptor 0, 1 or 2 that was closed when the shim started is
//     /dev/null, because the Go runtime opens it.
//   - The argument list and environment together are limited to
//     processshim.MaxRequestBytes, below the kernel's limit.
//   - The invocation path is matched lexically: a path reached through a
//     symlink the table does not declare fails with 127.
//   - After a stdin write whose outcome is uncertain, the broker asks the
//     service how much stdin it accepted and sends only the rest. When the
//     service cannot say, the shim exits with 255 and the program is
//     cancelled.
//   - A Cancel whose outcome is uncertain after a lost stream is not sent
//     again, because a second Cancel would send the scope TERM again. A
//     program that Cancel never reached keeps running until it exits or the
//     Session ends.
//   - A broker lost after the acknowledgement makes the shim exit with 255,
//     with the reason on its stderr when the relay can still write it. A
//     relay lost after the acknowledgement makes the shim exit with 255 and
//     no message, because the shim no longer holds its stderr.
//   - A pipe, FIFO or pty slave must be open for the direction the program
//     uses it in; the shim fails with 126 otherwise. Reads and writes of a
//     regular file or block device block, as a native program's do. A
//     character device other than a pty slave, such as /dev/tty, and a pipe,
//     FIFO or pty slave that the Session user may not reopen, is shared: the
//     relay polls it before each call and writes at most PIPE_BUF bytes at
//     once, and a read still waits when another reader took the data the
//     poll reported.
//   - Output on an AF_UNIX socket names the relay's pid, uid and gid, not
//     the shim's.
//   - A signal sent to the shim reaches the remote program only when the
//     process service declares it. The shim catches every signal a Go
//     program can catch except CHLD, PIPE, URG and PROF, and the broker drops
//     the ones the service does not declare. Of the signals it does not
//     catch, KILL ends the shim, ILL, TRAP, BUS, FPE, SEGV, STKFLT and SYS
//     make the Go runtime end it with status 2, and signals 32 and 34 end it;
//     a shim ended before the exit cancels the operation. PIPE, PROF and
//     signal 33 have no effect.
//   - A signal ignored when the shim started is still forwarded unless it is
//     HUP or INT, because the Go runtime replaces inherited ignores.
//   - A signal sent to the shim's PID reaches the remote initial process
//     group, or on a terminal the foreground process group for INT, QUIT,
//     TSTP, TTIN, TTOU, CONT and HUP, because the shim cannot tell it from a
//     signal sent to its process group.
package processbroker
