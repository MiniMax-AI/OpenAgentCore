// Package processbroker runs a Session's shim invocations in its sandbox.
//
// A Harness in the Session view executes oac-process-shim
// (apps/daemon/internal/processshim), which hands the broker its invocation
// and its descriptors 0, 1 and 2. The broker resolves the invocation against
// the declared executable table and environment policy, starts the program
// with the process protocol (internal/sandboxprocess), pumps its streams
// through the passed descriptors, forwards the signals the shim reports, and
// sends the shim the remote exit. The broker does all protocol work; the shim
// holds no credentials.
//
// The broker authenticates each connection with SO_PEERCRED, accepts only the
// view's uid, and never reads an identity from the payload. It never changes
// the flags of a passed descriptor, whose open file description the Harness
// shares. It reopens a pipe, FIFO or character device through /proc/self/fd
// as its own non-blocking description, uses a socket with MSG_DONTWAIT, and
// uses a regular file or block device as it is, so it waits on a peer only in
// a poll that ending the invocation interrupts.
//
// Each output descriptor closes after its stream's last byte, so a remote
// background job that keeps its output open keeps the Harness's pipe open
// while the shim still exits when the leader does. After the leader exits,
// remote background processes stay with the Session and are not cancelled;
// a shim lost before the exit cancels the operation's scope.
//
// Qualification limits. These behaviors differ from a native child:
//   - Stop and continue job control is incomplete. The shim reports TSTP,
//     TTIN and TTOU to the remote process group but does not stop itself, so
//     the Harness never sees the job stop. SIGSTOP of the shim stops only the
//     shim.
//   - Stdin is read ahead. The broker reads the shared stdin as data arrives
//     and forwards it, so bytes the program never consumes are still taken
//     from a stdin the Harness shares with later commands. Forwarding stops
//     when the leader exits; remote background readers then see end of file.
//   - On a terminal, stderr is merged into the terminal output, as the
//     remote PTY merges it.
//   - A descriptor 0, 1 or 2 that was closed when the shim started is
//     /dev/null, because the Go runtime opens it.
//   - The argument list and environment together are limited to
//     processshim.MaxFrameBytes, below the kernel's limit.
//   - The invocation path is matched lexically: a path reached through a
//     symlink the table does not declare fails with 127.
//   - A stdin write whose outcome is uncertain after a lost stream stops
//     stdin forwarding, because the write is never retried.
//   - A broker lost after the acknowledgement makes the shim exit with 255
//     and no message, because the shim no longer holds its stderr.
//   - Descriptors 0, 1 and 2 must each be a pipe, FIFO, socket, regular
//     file, block device, or character device other than /dev/tty,
//     /dev/console and /dev/ptmx, and a pipe, FIFO or character device must
//     be open for the direction the program uses it in. The shim fails with
//     126 otherwise. Reads and writes of a regular file or block device
//     block, as a native program's do.
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
