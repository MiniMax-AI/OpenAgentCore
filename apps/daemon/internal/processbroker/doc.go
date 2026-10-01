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
// the flags of a passed descriptor: they share open file descriptions with
// the Harness, so every read and write polls first and then uses the
// descriptor as it is.
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
package processbroker
