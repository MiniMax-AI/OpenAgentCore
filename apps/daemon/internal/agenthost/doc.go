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
// Sweep runs at startup, before any Session. It ends every task, each thread
// of each process, whose real, effective, saved or file-system uid lies in
// Config.UIDs, scans /proc again until none runs or a bound passes, and only
// then removes the Session directories a previous agent host left.
// Allocation also skips a uid that any running task holds. Session processes
// run only in a view's PID namespace, so Sweep ends a view's task by killing
// the namespace's init: the kernel then kills every process in the
// namespace, and nothing can fork into it any more. The init is the task's
// nearest ancestor whose pid in its namespace is 1; no process in a view can
// enter another namespace, so each ancestor up to it shares the namespace.
// Each step of the walk pins the parent with a pidfd and confirms that the
// child still has that parent, and each signal goes through a pidfd, so a
// reused pid is never followed or signalled. A task with a Session uid in
// the agent host's own namespace is not a Session's: Sweep kills its process
// and returns ErrTeardown if one still runs after the bound. A scan misses a
// process only while a parent that exits at once forks it; the view still
// ends once any of its tasks is caught, and a view's launcher ends the view
// when the agent host that started it goes away. A task that the agent
// host's /proc does not show, such as one in a sibling PID namespace, is
// outside these guarantees, which is why nothing else may use the range.
//
// The Harness view protocol is in contracts/agents-api/harness-onboarding.md
// and the gateway's in contracts/agents-api/model-execution.md.
package agenthost
