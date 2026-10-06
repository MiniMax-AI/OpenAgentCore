// Package agenthost runs Sessions whose Harness runs on the agent host, next to
// Core, while its tools, files and network act in the sandbox through the
// Session's Link attachment. It needs Linux; elsewhere Open returns
// ErrUnsupported.
//
// The process that runs the agent host calls Open once at startup, runs each
// Session with Host.Run and calls Close after the last Run has returned. Open
// takes two installation locks, exclusive flocks that the Host holds until
// Close: StateDir/lock allows one agent host per StateDir and its Session
// directories, and a flock on the Config.ViewCgroups directory allows one
// agent host per set of view cgroups. Under the locks, Open checks the
// requirements that create nothing, recovers what an earlier agent host left,
// checks that ViewCgroups can hold a new cgroup, and only then removes the
// Session directories, as Config describes, so nothing an earlier agent host
// left can block the checks. A step that fails fails Open, which then has
// reclaimed nothing. The cgroup hierarchy is the only record of what the views
// own: recovery ends each cgroup in ViewCgroups with every process in it, and
// identifies no process by name or credentials.
//
// The agent host requires root with the capabilities, /dev/fuse and the mount
// and seccomp support that sessionview.Probe checks, and a cgroup v2
// directory, Config.ViewCgroups, that is delegated to it, lies outside the
// agent host's own cgroup and can hold cgroups with cgroup.kill (Linux 5.14,
// which brings CLONE_INTO_CGROUP from Linux 5.7). Open checks each of these,
// without starting a process, and a missing one fails Open with ErrUnsupported
// and the sessionview error that names it; nothing falls back. A delegation
// that refuses only cloning a process into a view's cgroup shows when a view
// starts: its launch fails with ErrLaunch and sessionview.ErrLauncher.
//
// Host.Run runs one Session. It admits the Session before any effect: the kind
// must declare an agent.View, the request must use only what a view runs and
// no function tools, whose results Input cannot carry, and when the view
// declares shim names, which run on the sandbox PATH, the Session's
// Environment must set PATH. It then allocates the Session uid, skipping each
// uid that a running thread holds as its real, effective, saved or file-system
// uid; this check only detects a conflict and never ends a process. It creates
// the Session directory under Config.StateDir, rewrites the request so the
// model provider and HTTP MCP reach the network only through the Session's
// gateway, and calls the view's Executor factory. Each ViewSession.Launch
// builds one sessionview view in a cgroup of its own in Config.ViewCgroups, of
// which one at a time is live, over the world that worldfs serves from the
// attachment's File service, with the gateway listening in the view's network
// namespace. A view with a shim gets its own process broker, started once the
// view runs and closed once it has ended. The broker runs the shims' commands
// over the attachment's Process service in the strongest scope the service
// declares, with the view's ForwardEnv and the Session's Environment, and
// cancels a forwarded process whose shim is lost with the launch's kill
// timeout as its grace.
//
// Each view presents the closure directories read-only and executable, the
// Session home read-write and noexec, the agent host's /etc/passwd, group,
// hosts, resolv.conf and nsswitch.conf, the agent host's CA directory at its
// host path, then the adapter's overlays and masks and the process shim with
// its relay. Everything else is the world.
//
// The agent host owns the Session's Link attachment: it opens each stream
// with the Session's binding, renews the lease and fails the Session when the
// relay closes the attachment, a Link request fails in a way that is not
// retryable, the world is lost or did not stop cleanly, which leaves what the
// attachment holds uncertain, a view's process relay is lost while the view
// runs, or a view's teardown does not finish within sessionview's bound. A
// failure cancels the running Turn and closes the live view. A view's end is
// settled before its clirunner.Process reports it: the gateway and the
// process broker have stopped, the world's end is recorded and the view slot
// is free. Teardown releases, in order, the Executor, the view with its
// process broker, the Link attachment, the Session directory and the uid;
// Run decides its result only afterwards, so a failure recorded during
// teardown counts, and from the close of the attachment on, what the Link
// reports changes nothing. When Executor.Close fails, teardown ends the
// views, which kills their processes, and retries Close once. If Close
// fails again, the Executor may still use the Session directory: Run returns
// ErrTeardown and keeps the directory, and the uid stays in use until the
// agent host exits. A view whose teardown did not finish keeps both the same
// way, because its processes may still run, and keeps its cgroup for the next
// Open to recover.
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
// The Harness view protocol is in contracts/agents-api/harness-onboarding.md
// and the gateway's in contracts/agents-api/model-execution.md.
package agenthost
