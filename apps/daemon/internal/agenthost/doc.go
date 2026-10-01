// Package agenthost runs Sessions whose Harness runs on the agent host, next to
// Core, while its tools, files and network act in the sandbox through the
// Session's Link attachment. It needs Linux; elsewhere Run and Sweep return
// ErrUnsupported.
//
// Run runs one Session. It admits the Session before any effect: the kind
// must declare an agent.View, and the request must use only what a view runs
// and no function tools, whose results Input cannot carry.
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
// reports changes nothing. When Executor.Close fails, teardown ends the
// views, which kills their processes, and retries Close once. If Close
// fails again, the Executor may still use the Session directory: Run returns
// ErrTeardown and keeps the directory and the uid, which stays in use until
// the agent host exits, and the next agent host's Sweep reclaims both.
//
// Run drives each Turn as the daemon's dispatch drives a prepared execution.
// One output consumer starts before StartTurn and forwards the Turn's
// envelopes to Output in order. The Turn's Done waits until the Turn has
// settled and, when the Turn leaves the Executor unusable, until the
// Executor has closed; a failed Turn publishes an Error envelope before it.
// When Close fails, nothing more is published. A Turn that fails or leaves
// the Executor unusable ends the Session, and nothing is sent to Output
// after Run returns.
//
// Sweep runs at startup, before any Session. Session processes run only in
// views, and each view's launcher is PID 1 of the view's PID namespace, so
// Sweep first ends every view a previous agent host left with
// sessionview.EndLeftoverViews: killing a launcher kills every process in its
// view, whatever its uids, and the launcher exits only once its view is
// empty. Nothing in a view can start a view or leave its namespace, so no
// Session process remains once every launcher has exited. Sweep then kills
// each process with a task, any thread, whose real, effective, saved or
// file-system uid lies in Config.UIDs. Such a process runs outside every view
// and is not a Session's: Sweep scans /proc again until none runs or a bound
// passes and then returns ErrTeardown; one that forks and exits as each scan
// passes can escape the scans. Only then does Sweep remove the Session
// directories a previous agent host left. Each signal goes through a pidfd,
// so a reused pid is never signalled. Allocation also skips a uid that any
// running task holds. A view or task that the agent host's /proc does not
// show, such as one in a sibling PID namespace, is outside these guarantees,
// which is why nothing else may use the range or start views.
//
// The Harness view protocol is in contracts/agents-api/harness-onboarding.md
// and the gateway's in contracts/agents-api/model-execution.md.
package agenthost
