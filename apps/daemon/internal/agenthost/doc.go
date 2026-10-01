// Package agenthost runs Sessions whose Harness runs on the agent host, next to
// Core, while its tools, files and network act in the sandbox through the
// Session's Link attachment. It needs Linux; elsewhere Run and Sweep return
// ErrUnsupported.
//
// Run runs one Session. It admits the Session before any effect: the kind
// must declare an agent.View, and the request must use only what a view runs.
// It then allocates the Session uid, creates the Session directory under
// Config.StateDir, rewrites the request so the model provider and HTTP MCP
// reach the network only through the Session's gateway, and calls the view's
// Executor factory. The first ViewSession.Launch starts the process broker;
// each Launch builds one sessionview view, of which one at a time is live,
// over the world that worldfs serves from the attachment's File service, with
// the gateway listening in the view's network namespace.
//
// Each view presents the closure directories read-only and executable, the
// Session home read-write and noexec, the broker's run directory read-only,
// the agent host's /etc/passwd, group, hosts, resolv.conf and nsswitch.conf,
// the agent host's CA directory at its host path, then the adapter's overlays
// and masks and the process shim. Everything else is the world.
//
// The agent host owns the Session's Link attachment: it opens each stream
// with the Session's binding, renews the lease and fails the Session when the
// relay closes the attachment, a Link request fails in a way that is not
// retryable, or the world is lost or did not stop cleanly, which leaves what
// the attachment holds uncertain. A failure cancels the running Turn and
// closes the live view. A view's end is settled before its clirunner.Process
// reports it: the gateway has stopped, the world's end is recorded and the
// view slot is free. Teardown releases, in order, the Executor, the view, the
// process broker, the Link attachment, the Session directory and the uid;
// Run decides its result only afterwards, so a failure recorded during
// teardown counts, and from the close of the attachment on, what the Link
// reports changes nothing.
//
// Sweep runs at startup, before any Session. It sends SIGKILL to every
// process whose real, effective, saved or file-system uid lies in
// Config.UIDs, scans /proc again until none remains or a bound passes, and
// then removes the Session directories a previous agent host left. Allocation
// also skips a uid that a running process holds. A process in the range runs
// with no capabilities and no_new_privs, so it can only fork more processes
// of its own uid: each scan finds what the previous round's processes
// started, and a uid that no process holds at allocation stays free until the
// Session starts one. The signal goes through a pidfd and only after the
// uids are read again, so a pid reused since the scan is never signalled. A
// zombie runs nothing and is ignored. A process that the agent host's /proc
// does not show, such as one in a sibling PID namespace, is outside these
// guarantees, which is why nothing else may use the range.
//
// The Harness view protocol is in contracts/agents-api/harness-onboarding.md
// and the gateway's in contracts/agents-api/model-execution.md.
package agenthost
