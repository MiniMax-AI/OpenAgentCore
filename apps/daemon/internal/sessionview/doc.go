// Package sessionview runs one process inside a per-Session view on the agent host.
//
// A view is a private mount, PID and network namespace whose root is the Session's world: a FUSE file system that the daemon serves over a /dev/fuse connection. The launcher adds the local pieces on top of the world: private directories under /.oac, trusted overlays, the command shim, a fresh /proc and a minimal /dev. The world presents a mountpoint for each piece and reports where, following the sandbox's symlinks, and the launcher mounts at those paths without following any symlink itself. The process starts with no capabilities, no_new_privs, a seccomp filter and only stdin, stdout and stderr open. Its network namespace has only loopback up.
//
// The exec guard is narrow. Mount flags alone decide which files can be executed: the world and every writable mount are nosuid and noexec, so executing a file from the file system works only from read-only mounts declared executable, such as the Harness directory, the shim and exec-flagged overlays. The seccomp filter denies creating a user namespace and every setns, so the process cannot create or enter another user namespace. Executing from a memfd and code that an allowed interpreter runs are outside this guard.
//
// The daemon calls [Init] first thing in main. [Start] re-executes the daemon binary as the launcher, which becomes PID 1 of the view: it builds the view, starts the process, delivers signals to every process in the view, reaps orphans and exits with the process status. Once the process has exited, Signal reports [ErrExited] and delivers nothing. When the process exits while others remain, the launcher sends them TERM unless one already went to the view, and waits for them up to [Process].Grace from the first TERM. Its exit kills what remains and tears the view down.
//
// The package works only on Linux. Elsewhere [Start] returns [ErrUnsupported].
package sessionview
