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
// retryable, or the world is lost or may still hold state. A failure cancels
// the running Turn and closes the live view. Teardown releases, in order, the
// Executor, the view, the process broker, the Link attachment, the Session
// directory and the uid. Sweep removes the Session directories a previous
// agent host left; Run's owner calls it at startup.
//
// The Harness view protocol is in contracts/agents-api/harness-onboarding.md
// and the gateway's in contracts/agents-api/model-execution.md.
package agenthost
