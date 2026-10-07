// Package agenthost runs Sessions whose Harness runs on the agent host, next to
// Core, while its tools, files and network act in the sandbox through the
// Session's Link attachment. It needs Linux; elsewhere Open returns
// ErrUnsupported.
//
// The process that runs the agent host calls Open once at startup, hands
// Host.Registry and Host.Environments to the daemon's dispatch, which drives
// each Turn of each Executor, and calls Close once every Executor has closed. No production
// caller constructs Host.Registry yet; oac-daemon connect still runs
// Harnesses in the sandbox. Open takes two installation locks, which the
// Host holds until Close: a flock on StateDir/lock for the Session
// directories and one on the Config.ViewCgroups directory for the view
// cgroups. Under the locks, Open checks the requirements that need nothing
// created, ends every cgroup in ViewCgroups with all its processes
// (sessionview.Recover), checks the requirements that create something, and
// only then sweeps the Session directories: it removes their transient
// entries and keeps their homes. So no process of an earlier Executor still
// uses a directory or uid that a new Executor gets, and nothing an earlier
// agent host left can fail a check. If a step fails, Open fails and has
// reclaimed nothing. The cgroup hierarchy is the only record of what the
// views own; no process is identified by name or credentials.
//
// The agent host requires root with the capabilities, /dev/fuse and the mount
// and seccomp support that sessionview.Probe checks; clone3, which some
// seccomp filters block; and a writable cgroup v2 directory,
// Config.ViewCgroups, delegated to it and outside its own cgroup, that can
// hold cgroups with cgroup.kill (Linux 5.14). Open checks each without
// starting a process, and a missing one fails Open with ErrUnsupported;
// nothing falls back. The kernel's common-ancestor and cgroup-namespace
// checks on a delegation run only when a process is cloned into a cgroup, so
// a delegation that fails them fails each view's launch with ErrLaunch and
// sessionview.ErrLauncher.
//
// Host.Environments gives each Session bound to the agent host one
// Environment owner, a dispatch.Environment, from its first assignment_bind
// until Host.RemoveHome, so the owner outlives Executors and Routers. It
// opens its own Link attachment on first use and closes it when dispatch
// closes the owner at release, quiescence or shutdown; its next operation
// opens a new one. Over the attachment's File service it applies
// runtime_prepare to the sandbox: initial files in /workspace, the frozen
// tool environment and the completion marker in /environment/initialization,
// and Skills, plugins and the declared directories' snapshot in
// agentcapabilities.Directory. A setup, npm or python step runs over the
// Process service as the operation the transfer's ID names. Before each
// Executor factory runs, the owner checks the installation, without
// initializing a completed one again, and fills the request's Skills, MCP and
// capability root as sandbox paths. It also serves workspace reads, Files
// create and outputs export. A File mutation or setup step whose effect is
// unknown quarantines the owner: it sends no mutation again until
// Host.RemoveHome forgets it. A Session with environment none gets an owner
// without an Environment, which never attaches.
//
// Host.Registry's Executor factory prepares an Executor of the Session whose
// owner prepared the request, with the sandbox's baseline environment and the
// frozen tool environment as its Environment. It admits the request before
// any effect: the kind must declare an agent.View, and when the view declares
// shim names or a stdio MCP server's command is a bare name, both of which
// run on the sandbox PATH, the Session's Environment must set PATH. A
// Session with Skills, with a restricted network, or with a stdio MCP server
// that needs a credential is rejected. The registry declares each kind in a
// local Environment and with environment none, as agent.EnvironmentSupport
// composes them. The factory then allocates the
// Executor's uid, skipping each uid that a running thread holds as its real,
// effective, saved or file-system uid; this check only detects a conflict
// and never ends a process. It prepares the Session directory under
// Config.StateDir, rewrites the request so the model provider and HTTP MCP
// reach the network only through the Session's gateway and each stdio MCP
// server runs under its alias, agent.ViewAlias, and calls the view's
// Executor factory. Each ViewSession.Launch gives the Session home to
// the Executor's uid and builds one sessionview view, of which one at a time
// is live, over the world that worldfs serves from the attachment's File
// service, with the gateway listening in the view's network namespace.
// ViewSession.Spawn runs another process in the live view
// (sessionview.View.Spawn). A view with a shim gets its own process broker,
// started once the view runs and closed once it has ended. The broker runs
// the shims' commands over the attachment's Process service in the
// strongest scope the service declares, with the view's ForwardEnv and the
// Session's Environment, and cancels a forwarded process whose shim is lost
// with the launch's kill timeout as its grace. An alias runs its stdio MCP
// server's frozen command, arguments and working directory, a relative one
// in the installation's package root, with only the Session's Environment.
//
// A Session with environment none has no sandbox. Its views are empty-root
// views: no world, no shims, no process broker and no sandbox network, so
// the gateway's generic proxy refuses every request, and the Harness runs in
// the home's work directory. Such a Session never opens its Link attachment.
//
// Each view presents the closure directories read-only and executable, the
// Session home read-write and noexec, the agent host's /etc/passwd, group,
// hosts, resolv.conf and nsswitch.conf, the agent host's CA directory at its
// host path, then the adapter's overlays and masks and the process shim with
// its relay. Everything else is the world, or nothing in an empty-root view.
//
// The Session directory, StateDir/sessions/<Session ID>, stays root-owned
// and private. Its home holds the Harness's native history and persists
// across the Session's Executors and the agent host's restarts until
// Host.RemoveHome removes it. Its other entries are transient: each Executor
// creates them. The Host claims a Session in memory from an Executor's open
// until its Close succeeds, and while RemoveHome runs, so one of them at a
// time uses the directory.
//
// Each Executor owns its own Link attachment: it opens each stream with the
// Session's binding, renews the lease and fails the Session when the relay
// closes the attachment, a Link request fails in a way that is not
// retryable, the world is lost or did not stop cleanly, which leaves what
// the attachment holds uncertain, a view's process relay is lost while the
// view runs, or a view's teardown does not finish within sessionview's
// bound. A failure is logged and closes the live view, which ends the
// running Turn, and later launches fail. A view's end is settled before its
// clirunner.Process reports it: the gateway and the process broker have
// stopped, the world's end is recorded and the view slot is free.
// Executor.Close releases, in order, the view Executor, the views with their
// process brokers, the Link attachment, the transient entries, and the uid
// with the Session's claim. When the view Executor's Close fails, Close ends
// the views, which kills their processes, and retries it once. If the view
// Executor still fails, or the relay does not confirm the attachment's close,
// Close returns ErrTeardown and keeps the transient entries, the uid and the
// claim until a later Close succeeds. A view whose teardown did not finish
// may leave processes that use the Session directory, so every Close then
// returns ErrTeardown and the uid and the claim stay until the agent host
// exits.
//
// The Harness view protocol is in contracts/agents-api/harness-onboarding.md
// and the gateway's in contracts/agents-api/model-execution.md.
package agenthost
