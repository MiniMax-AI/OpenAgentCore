//go:build linux

package agenthost

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// The sandbox layout an Environment owner prepares. Providers create the
// initialization and package directories for the sandbox's user.
const (
	logicalWorkspace      = "/workspace"
	sandboxInitialization = "/environment/initialization"
	sandboxPackages       = "/environment/packages"
	toolEnvironmentName   = "tool-env.json"
	markerBytes           = 8192
	toolEnvironmentBytes  = 1 << 20
	// writeBound bounds a workspace write, which dispatch never cancels.
	writeBound = time.Minute
)

// sandboxBaseline is the environment of the sandbox image that a Session's
// processes in the sandbox start from, never the agent host's own.
var sandboxBaseline = map[string]string{"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME": "/home/runtime", "LANG": "C.UTF-8"}

// owners holds the Environment owner of each Session bound to the agent
// host, from its first bind until RemoveHome.
type owners struct {
	d  deps
	mu sync.Mutex
	m  map[sandboxwire.ID]*environment
}

// environment is a Session's Environment owner on the agent host. It
// prepares and serves the sandbox through File and Process on its own Link
// attachment, which it opens on first use and drains on Close; a later
// operation opens a new one. An uncertain mutation quarantines it: it sends
// no mutation again while it lives, across drains and Routers.
type environment struct {
	d         deps
	session   string // the canonical Session ID
	id        string // the Environment ID; empty for environment none
	workspace string // the immutable physical workspace from assignment_bind
	// sem serializes the owner's operations, Close included. Its holder
	// owns every field below; a rebind also holds owners.mu.
	sem       chan struct{}
	binding   Binding
	link      *linkOwner
	lost      *atomic.Bool // the link reported a failure of the attachment
	world     *world
	uncertain bool
	// tool is the tool environment the last preparation read.
	tool map[string]string
}

// Environments resolves the Environment owner of a Session's first bind on a
// Router, as dispatch.Config.Environments: it does no I/O. A Session keeps
// its owner across Routers until RemoveHome. A bind under another assignment
// takes the owner over once it is drained. Environments returns nil for a
// bind the agent host does not serve.
func (h *Host) Environments(ref proto.AssignmentRef, bind proto.AssignmentBindPayload) dispatch.Environment {
	session, err := canonicalID(ref.SessionID)
	assignment, err2 := canonicalID(ref.AssignmentID)
	if err != nil || err2 != nil || bind.Validate() != nil {
		return nil
	}
	if bind.EnvironmentID != "" && (bind.Resource == nil || !isViewPath(bind.WorkspaceDirectory) || checkLayout(h.cfg, agent.View{}, bind.WorkspaceDirectory) != nil) {
		return nil
	}
	b := Binding{SessionID: session, AssignmentID: assignment, AssignmentEpoch: ref.Epoch, AttachGrant: slices.Clone(bind.AttachGrant)}
	if bind.Resource != nil {
		b.Resource = bind.Resource.Ref()
	}
	h.owners.mu.Lock()
	defer h.owners.mu.Unlock()
	o := h.owners.m[session]
	switch {
	case o == nil:
		o = &environment{d: h.owners.d, session: ref.SessionID, id: bind.EnvironmentID, workspace: bind.WorkspaceDirectory, sem: make(chan struct{}, 1), binding: b}
		if h.owners.m == nil {
			h.owners.m = map[sandboxwire.ID]*environment{}
		}
		h.owners.m[session] = o
	case o.id != bind.EnvironmentID || o.workspace != bind.WorkspaceDirectory:
		return nil
	case !sameBinding(o.binding, b):
		select {
		case o.sem <- struct{}{}:
		default:
			return nil
		}
		drained := o.link == nil
		if drained {
			o.binding = b
		}
		<-o.sem
		if !drained {
			return nil
		}
	}
	return o
}

func sameBinding(a, b Binding) bool {
	return a.Resource == b.Resource && a.SessionID == b.SessionID && a.AssignmentID == b.AssignmentID &&
		a.AssignmentEpoch == b.AssignmentEpoch && bytes.Equal(a.AttachGrant, b.AttachGrant)
}

func canonicalID(s string) (sandboxwire.ID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil || id.String() != s {
		return sandboxwire.ID{}, fmt.Errorf("%q is not a canonical UUID", s)
	}
	return sandboxwire.ID(id), nil
}

// executor returns the binding and Environment of an Executor of req's
// Session, whose preparation the owner prepared.
func (h *Host) executor(ctx context.Context, req agent.PrepareRequest) (Binding, Environment, error) {
	session, err := canonicalID(req.Assignment.SessionID)
	if err != nil {
		return Binding{}, Environment{}, invalidSession("binding: %v", err)
	}
	h.owners.mu.Lock()
	o := h.owners.m[session]
	h.owners.mu.Unlock()
	if o == nil {
		return Binding{}, Environment{}, invalidSession("binding: the Session has no Environment owner")
	}
	if err := o.acquire(ctx); err != nil {
		return Binding{}, Environment{}, err
	}
	defer o.release()
	b := o.binding
	switch {
	case uuid.UUID(b.AssignmentID).String() != req.Assignment.AssignmentID || b.AssignmentEpoch != req.Assignment.Epoch:
		return Binding{}, Environment{}, invalidSession("binding: the request's assignment is not the Session's")
	case o.id == "":
		return b, Environment{}, nil
	case o.tool == nil:
		return Binding{}, Environment{}, invalidSession("binding: the Environment is not prepared")
	}
	return b, Environment{Sandbox: maps.Clone(sandboxBaseline), Tool: maps.Clone(o.tool)}, nil
}

// dropEnvironment drains and forgets the Session's owner.
func (h *Host) dropEnvironment(session sandboxwire.ID) error {
	h.owners.mu.Lock()
	o := h.owners.m[session]
	h.owners.mu.Unlock()
	if o == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeBound)
	defer cancel()
	if err := o.Close(ctx); err != nil {
		return err
	}
	h.owners.mu.Lock()
	if h.owners.m[session] == o {
		delete(h.owners.m, session)
	}
	h.owners.mu.Unlock()
	return nil
}

func (o *environment) acquire(ctx context.Context) error {
	select {
	case o.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *environment) release() { <-o.sem }

func (o *environment) identity() agentcapabilities.Identity {
	return agentcapabilities.Identity{EnvironmentID: o.id, SessionID: o.session}
}

// attach returns the owner's world, opening an attachment when it has none
// or the last one failed. The holder of sem calls it.
func (o *environment) attach(ctx context.Context) (*world, error) {
	if o.world != nil && !o.world.ended() && !o.lost.Load() {
		return o.world, nil
	}
	if err := o.drain(); err != nil {
		return nil, err
	}
	lost := new(atomic.Bool)
	// The link calls fail under its lock, so fail only records the loss.
	o.link, o.lost = newLinkOwner(o.d.dial, o.binding, sandboxwire.NewID(), func(error) { lost.Store(true) }), lost
	st, err := o.link.open(ctx, sandboxlink.ServiceFile, sandboxfs.Version)
	if err != nil {
		return nil, err
	}
	if o.world, err = attachWorld(ctx, st, &o.uncertain); err != nil {
		return nil, fmt.Errorf("%w: attach the world: %w", ErrWorld, err)
	}
	return o.world, nil
}

// drain closes the owner's attachment. It keeps the attachment when the
// relay did not confirm its close, so that a later drain retries.
func (o *environment) drain() error {
	if o.world != nil {
		o.world.c.Close()
		o.world = nil
	}
	if o.link == nil {
		return nil
	}
	if err := o.link.close(); err != nil {
		return err
	}
	o.link, o.lost = nil, nil
	return nil
}

// done ends an operation on w: it forgets the references the operation
// acquired. A stream that fails here is drained on next use.
func (o *environment) done(w *world) {
	ctx, cancel := context.WithTimeout(context.Background(), closeBound)
	defer cancel()
	w.forget(ctx)
}

// Close drains the owner. It keeps its quarantine and serves again on next
// use.
func (o *environment) Close(ctx context.Context) error {
	if err := o.acquire(ctx); err != nil {
		return err
	}
	defer o.release()
	return o.drain()
}

// Configure checks the request against the owner's Environment, which
// dispatch resolved from the Session's assignment.
func (o *environment) Configure(r proto.PromptRequestPayload) error {
	local := r.LocalEnvironment
	switch {
	case o.id == "":
		if local != nil || !r.DisableExecutionEnvironment {
			return errors.New("the Session has no Environment")
		}
		return nil
	case local == nil || r.DisableExecutionEnvironment || local.ID != o.id:
		return errors.New("the request does not name the Session's Environment")
	case r.WorkspaceReadOnly:
		return nil
	case local.CapabilitySources == nil || agentcapabilities.ValidateInput(*local.CapabilitySources) != nil:
		return agentcapabilities.ErrInvalid
	}
	return nil
}

// Prepare completes the Session's installation when Core sent no finalize,
// checks it against the frozen selection, and fills the request's workspace
// root, Skills, MCP and capability root as sandbox paths.
func (o *environment) Prepare(ctx context.Context, r agent.PrepareRequest) (agent.PrepareRequest, error) {
	if r.WorkspaceReadOnly || o.id == "" && r.LocalEnvironment == nil {
		return r, nil
	}
	if o.id == "" || r.LocalEnvironment == nil || r.LocalEnvironment.ID != o.id || r.LocalEnvironment.CapabilitySources == nil {
		return r, agentcapabilities.ErrInvalid
	}
	if err := o.acquire(ctx); err != nil {
		return r, err
	}
	defer o.release()
	w, err := o.attach(ctx)
	if err != nil {
		return r, err
	}
	defer o.done(w)
	local := r.LocalEnvironment
	identity := o.identity()
	name, body, err := agentcapabilities.Marker(identity, agentcapabilities.Directory)
	if err != nil {
		return r, err
	}
	initialization, err := w.directory(ctx, w.root, sandboxInitialization, true)
	if err != nil {
		return r, err
	}
	completed, err := w.marker(ctx, initialization, name, body)
	if err != nil {
		return r, err
	}
	values, err := o.toolEnvironment(ctx, w, initialization, local.ToolEnvironment, completed)
	if err != nil {
		return r, err
	}
	root, err := w.directory(ctx, w.root, agentcapabilities.Directory, !completed)
	if err != nil {
		return r, err
	}
	manifest, err := o.snapshot(ctx, w, root, *local.CapabilitySources, identity, completed)
	if err != nil {
		return r, err
	}
	if !completed {
		if err := w.publish(ctx, initialization, name, 0o600, body, false); err != nil {
			return r, err
		}
	}
	var mcp []agent.EnvironmentMCP
	if len(manifest.MCP) != 0 {
		tokens, err := agentcapabilities.ResolveMCP(manifest.MCP, values)
		if err != nil {
			return r, err
		}
		for i, item := range manifest.MCP {
			mcp = append(mcp, agent.EnvironmentMCP{InstallationRoot: agentcapabilities.Directory, WorkspaceRoot: o.workspace,
				PackageRoot: item.PackageRoot, Server: item.Server, BearerToken: tokens[i]})
		}
	}
	for i := range manifest.Skills {
		manifest.Skills[i].InstallationRoot = agentcapabilities.Directory
	}
	o.tool = values
	r.WorkspaceRoot, r.CapabilityRoot, r.Skills, r.MCP = o.workspace, agentcapabilities.Directory, manifest.Skills, mcp
	return r, nil
}

// ApplyRuntimePreparation applies one runtime_prepare transfer to the
// sandbox. A setup step runs as the Process operation named transfer. A
// failure to reach the sandbox before any effect is
// dispatch.ErrEnvironmentUnavailable.
func (o *environment) ApplyRuntimePreparation(ctx context.Context, transfer uuid.UUID, input proto.RuntimePreparePayload, data []byte) error {
	if o.id == "" || input.EnvironmentID != o.id || input.SessionID != o.session || !proto.ValidRuntimePrepareRequest(input) || input.Step != "begin" {
		return agentcapabilities.ErrInvalid
	}
	if err := o.acquire(ctx); err != nil {
		return dispatch.ErrEnvironmentUnavailable
	}
	defer o.release()
	if o.uncertain {
		return errUncertain
	}
	w, err := o.attach(ctx)
	if err != nil {
		return dispatch.ErrEnvironmentUnavailable
	}
	defer o.done(w)
	identity := o.identity()
	name, body, err := agentcapabilities.Marker(identity, agentcapabilities.Directory)
	if err != nil {
		return err
	}
	initialization, err := w.directory(ctx, w.root, sandboxInitialization, true)
	if err != nil {
		return failed(err)
	}
	if completed, err := w.marker(ctx, initialization, name, body); err != nil || completed {
		return agentcapabilities.ErrInvalid
	}
	switch input.Action {
	case "file":
		if len(data) != input.SizeBytes {
			return agentcapabilities.ErrInvalid
		}
		return o.installFile(ctx, w, input.File.Path, data)
	case "initialize":
		if len(data) != 0 {
			return agentcapabilities.ErrInvalid
		}
		return o.initialize(ctx, w, initialization, sandboxwire.ID(transfer), *input.Initialization)
	}
	root, err := w.directory(ctx, w.root, agentcapabilities.Directory, true)
	if err == nil {
		switch input.Action {
		case "skill":
			var tree agentcapabilities.Tree
			if tree, err = agentcapabilities.SkillTree(data, *input.Skill); err == nil {
				err = w.stage(ctx, root, tree)
			}
		case "plugin":
			var tree agentcapabilities.Tree
			if tree, err = agentcapabilities.PluginTree(input.Slot, data, *input.Plugin); err == nil {
				err = checkPluginCredentials(tree)
			}
			if err == nil {
				err = w.stage(ctx, root, tree)
			}
		case "finalize":
			if _, err = o.toolEnvironment(ctx, w, initialization, false, false); err == nil {
				if _, err = o.snapshot(ctx, w, root, *input.Sources, identity, false); err == nil {
					err = w.publish(ctx, initialization, name, 0o600, body, false)
				}
			}
		default:
			err = agentcapabilities.ErrInvalid
		}
	}
	return failed(err)
}

// failed returns err as the outcome of a step that failed: a mutation whose
// effect is unknown stays unknown, and anything else failed.
func failed(err error) error {
	if err == nil || errors.Is(err, errUncertain) {
		return err
	}
	return agentcapabilities.ErrInvalid
}

// initializationFailed is failed for a setup step, which fails with an
// InitializationFailure.
func initializationFailed(err error) error {
	if err == nil || errors.Is(err, errUncertain) {
		return err
	}
	return &dispatch.InitializationFailure{}
}

// checkPluginCredentials refuses a plugin whose MCP server declares literal
// headers, which would put a credential in the world, or is a stdio server
// that takes credentials from the Environment, which no view runs.
func checkPluginCredentials(tree agentcapabilities.Tree) error {
	bundle, err := agentplugin.Inspect(tree.Files)
	if err != nil {
		return agentcapabilities.ErrInvalid
	}
	for _, server := range bundle.MCP {
		if len(server.HTTPHeaders) != 0 || server.Type == "stdio" && agent.EnvironmentMCPCredentials(server) {
			return agentcapabilities.ErrInvalid
		}
	}
	return nil
}

func (o *environment) installFile(ctx context.Context, w *world, target string, data []byte) error {
	relative, ok := strings.CutPrefix(target, logicalWorkspace+"/")
	if !ok || !proto.ValidWorkspacePath(relative) || len(data) > proto.RuntimePrepareMaxBytes {
		return agentcapabilities.ErrInvalid
	}
	workspace, err := w.directory(ctx, w.root, o.workspace, false)
	if err != nil {
		return initializationFailed(err)
	}
	parent, err := w.directory(ctx, workspace, path.Dir(relative), true)
	if err != nil {
		return initializationFailed(err)
	}
	// Initial files replace what the path holds, unlike Files create.
	return initializationFailed(w.publish(ctx, parent, path.Base(relative), 0o600, data, true))
}

func (o *environment) initialize(ctx context.Context, w *world, initialization sandboxfs.NodeRef, id sandboxwire.ID, input proto.RuntimeInitialization) error {
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > proto.RuntimePrepareMaxFrameBytes {
		return agentcapabilities.ErrInvalid
	}
	program := map[string]string{"setup": "bash", "npm": "npm", "python": "python3"}[input.Action]
	switch {
	case input.Action == "configure":
		return o.configure(ctx, w, initialization, input.Env)
	case program == "":
		return agentcapabilities.ErrInvalid
	}
	cwd := o.workspace
	if input.CWD != "" && input.CWD != logicalWorkspace {
		relative, ok := strings.CutPrefix(input.CWD, logicalWorkspace+"/")
		if !ok || !proto.ValidWorkspacePath(relative) {
			return agentcapabilities.ErrInvalid
		}
		cwd = path.Join(o.workspace, relative)
	}
	values, err := w.toolEnvironment(ctx, initialization)
	if err != nil {
		return &dispatch.InitializationFailure{}
	}
	args, err := agentcapabilities.InitializationArgs(input.Action, input.Command, input.Packages, sandboxPackages+"/npm", sandboxPackages+"/python")
	if err != nil {
		return err
	}
	if input.Action != "setup" {
		if _, err := w.directory(ctx, w.root, sandboxPackages, true); err != nil {
			return initializationFailed(err)
		}
	}
	env := maps.Clone(sandboxBaseline)
	maps.Copy(env, values)
	return o.run(ctx, id, program, args, env, cwd)
}

// configure freezes the tool environment: values with the package
// directories ahead of the baseline PATH.
func (o *environment) configure(ctx context.Context, w *world, initialization sandboxfs.NodeRef, values map[string]string) error {
	if !agentcapabilities.ValidToolEnvironment(values, false) {
		return agentcapabilities.ErrInvalid
	}
	configured := agentcapabilities.ToolEnvironment(values, sandboxBaseline["PATH"], sandboxPackages+"/npm/bin", sandboxPackages+"/python/bin",
		sandboxPackages+"/python", ":")
	raw, err := json.Marshal(configured)
	if err != nil || len(raw) > proto.RuntimePrepareMaxFrameBytes {
		return agentcapabilities.ErrInvalid
	}
	if _, err := w.directory(ctx, w.root, sandboxPackages, true); err != nil {
		return initializationFailed(err)
	}
	return initializationFailed(w.publish(ctx, initialization, toolEnvironmentName, 0o600, raw, false))
}

// toolEnvironment returns the frozen tool environment, freezing the default
// one when no step configured it and nothing requires it.
func (o *environment) toolEnvironment(ctx context.Context, w *world, initialization sandboxfs.NodeRef, required, completed bool) (map[string]string, error) {
	values, err := w.toolEnvironment(ctx, initialization)
	if !errors.Is(err, fs.ErrNotExist) {
		return values, err
	}
	if required || completed {
		return nil, errors.New("the prepared tool environment is unavailable")
	}
	if err := o.configure(ctx, w, initialization, nil); err != nil {
		return nil, err
	}
	return w.toolEnvironment(ctx, initialization)
}

// snapshot returns the installation's checked manifest, finalizing the
// installation first when it has none and is not complete.
func (o *environment) snapshot(ctx context.Context, w *world, root sandboxfs.NodeRef, input agentcapabilities.Input, identity agentcapabilities.Identity, completed bool) (agentcapabilities.Manifest, error) {
	entry, err := w.lookup(ctx, root, agentcapabilities.ManifestName)
	if isErrno(err, sandboxfs.ErrnoNotFound) && !completed {
		if err := w.finalize(ctx, root, input, identity); err != nil {
			return agentcapabilities.Manifest{}, failed(err)
		}
		entry, err = w.lookup(ctx, root, agentcapabilities.ManifestName)
	}
	if err != nil {
		return agentcapabilities.Manifest{}, agentcapabilities.ErrInvalid
	}
	body, err := w.readEntry(ctx, entry, agentcapabilities.MaxManifestBytes)
	if err != nil || entry.Attr.Mode&0o222 != 0 {
		return agentcapabilities.Manifest{}, agentcapabilities.ErrInvalid
	}
	manifest, err := agentcapabilities.Check(body, func(name string) ([]agentbundle.File, error) { return w.readTreeAt(ctx, root, name, true) })
	if err != nil || agentcapabilities.ValidateSelection(manifest, input, identity) != nil {
		return agentcapabilities.Manifest{}, agentcapabilities.ErrInvalid
	}
	return manifest, nil
}

// ListWorkspaceDirectory lists a directory of the sandbox workspace.
func (o *environment) ListWorkspaceDirectory(ctx context.Context, p string, limit int) (dispatch.WorkspaceDirectoryResult, error) {
	result := dispatch.WorkspaceDirectoryResult{Entries: []dispatch.WorkspaceDirectoryEntry{}}
	switch {
	case o.id == "":
		return result, dispatch.ErrWorkspaceReadUnavailable
	case p != "" && !proto.ValidWorkspacePath(p) || limit < 1:
		return result, dispatch.ErrWorkspaceReadInvalid
	}
	if err := o.acquire(ctx); err != nil {
		return result, dispatch.ErrWorkspaceReadUnavailable
	}
	defer o.release()
	w, err := o.attach(ctx)
	if err != nil {
		return result, dispatch.ErrWorkspaceReadUnavailable
	}
	defer o.done(w)
	workspace, err := w.directory(ctx, w.root, o.workspace, false)
	if err != nil {
		return result, dispatch.ErrWorkspaceReadUnavailable
	}
	dir, err := w.directory(ctx, workspace, p, false)
	var entries []sandboxfs.DirEntry
	if err == nil {
		entries, err = w.list(ctx, dir, limit)
	}
	switch {
	case isErrno(err, sandboxfs.ErrnoPermissionDenied):
		return result, fs.ErrPermission
	case errors.Is(err, errNotDirectory):
		return result, dispatch.ErrWorkspaceNotDirectory
	case err != nil:
		return result, dispatch.ErrWorkspaceReadUnavailable
	}
	result.Truncated = len(entries) > limit
	entries = entries[:min(len(entries), limit)]
	slices.SortFunc(entries, func(a, b sandboxfs.DirEntry) int { return bytes.Compare(a.Name, b.Name) })
	for _, e := range entries {
		entry := dispatch.WorkspaceDirectoryEntry{Name: string(e.Name), Kind: "other"}
		switch attr := e.Entry.Attr; attr.Mode & sandboxfs.ModeType {
		case sandboxfs.ModeRegular:
			size := int64(attr.Size)
			entry.Kind, entry.SizeBytes = "file", &size
		case sandboxfs.ModeDirectory:
			entry.Kind = "directory"
		case sandboxfs.ModeSymlink:
			entry.Kind = "symlink"
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, nil
}

// WriteWorkspaceFile creates a file in the sandbox workspace with complete
// bytes, never replacing what the path holds.
func (o *environment) WriteWorkspaceFile(ctx context.Context, p string, data []byte) (dispatch.WorkspaceWriteResult, error) {
	var result dispatch.WorkspaceWriteResult
	switch {
	case len(data) > proto.WorkspaceWriteMaxBytes || !proto.ValidWorkspacePath(p):
		return result, dispatch.ErrWorkspaceWriteInvalid
	case o.id == "":
		return result, dispatch.ErrEnvironmentUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, writeBound)
	defer cancel()
	if err := o.acquire(ctx); err != nil {
		return result, dispatch.ErrWorkspaceWriteBusy
	}
	defer o.release()
	if o.uncertain {
		return result, dispatch.ErrWorkspaceWriteUncertain
	}
	w, err := o.attach(ctx)
	if err != nil {
		return result, dispatch.ErrEnvironmentUnavailable
	}
	defer o.done(w)
	workspace, err := w.directory(ctx, w.root, o.workspace, false)
	if err != nil {
		return result, dispatch.ErrEnvironmentUnavailable
	}
	parent, err := w.directory(ctx, workspace, path.Dir(p), true)
	if err == nil {
		err = w.publish(ctx, parent, path.Base(p), 0o600, data, false)
	}
	switch {
	case err == nil:
		return dispatch.WorkspaceWriteResult{SizeBytes: int64(len(data))}, nil
	case errors.Is(err, errUncertain):
		return result, dispatch.ErrWorkspaceWriteUncertain
	case errors.Is(err, fs.ErrExist):
		if e, err := w.lookup(ctx, parent, path.Base(p)); err == nil && isType(e.Attr, sandboxfs.ModeDirectory) {
			return result, dispatch.ErrWorkspaceWriteDirectory
		}
		return result, dispatch.ErrWorkspaceWriteUnsafe
	}
	return result, dispatch.ErrWorkspaceWriteRejected
}

// ExportOutputs writes the workspace's outputs directory to out as a tar
// stream: regular files and directories, without symbolic links, within the
// export bounds.
func (o *environment) ExportOutputs(ctx context.Context, out io.Writer) error {
	if o.id == "" {
		return errors.New("the Session has no Environment")
	}
	if err := o.acquire(ctx); err != nil {
		return err
	}
	defer o.release()
	w, err := o.attach(ctx)
	if err != nil {
		return err
	}
	defer o.done(w)
	workspace, err := w.directory(ctx, w.root, o.workspace, false)
	if err != nil {
		return err
	}
	archive := tar.NewWriter(out)
	outputs, err := w.lookup(ctx, workspace, "outputs")
	switch {
	case isErrno(err, sandboxfs.ErrnoNotFound):
		return archive.Close()
	case err != nil:
		return err
	case !isType(outputs.Attr, sandboxfs.ModeDirectory):
		return errors.New("workspace outputs is not a directory")
	}
	x := export{w: w, archive: archive}
	if err := x.walk(ctx, outputs.Node, "outputs", 0); err != nil {
		return err
	}
	return archive.Close()
}

// The bounds of an output export: each file's bytes, the export's bytes, its
// entries and its directory depth.
const (
	exportFileBytes  int64 = 200 << 20
	exportBatchBytes int64 = 500 << 20
	exportEntries          = 4096
	exportDepth            = 64
)

// export walks the outputs directory over File into a tar archive.
type export struct {
	w       *world
	archive *tar.Writer
	entries int
	bytes   int64
}

func (x *export) walk(ctx context.Context, dir sandboxfs.NodeRef, name string, depth int) error {
	if depth > exportDepth {
		return errors.New("workspace export exceeds traversal bound")
	}
	entries, err := x.w.sorted(ctx, dir, exportEntries)
	if err != nil {
		return err
	}
	if x.entries += len(entries); x.entries > exportEntries {
		return errors.New("workspace export exceeds entry bound")
	}
	for _, e := range entries {
		child := name + "/" + string(e.Name)
		if !proto.ValidWorkspacePath(child) {
			return fs.ErrInvalid
		}
		switch e.Entry.Attr.Mode & sandboxfs.ModeType {
		case sandboxfs.ModeSymlink:
		case sandboxfs.ModeDirectory:
			err = x.walk(ctx, e.Entry.Node, child, depth+1)
		case sandboxfs.ModeRegular:
			err = x.append(ctx, *e.Entry, child)
		default:
			err = errors.New("workspace output is not a regular file")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (x *export) append(ctx context.Context, e sandboxfs.Entry, name string) error {
	h, err := x.w.open(ctx, e)
	if err != nil {
		return err
	}
	defer x.w.release(ctx, h)
	target := sandboxfs.Target{Kind: sandboxfs.TargetHandle, Handle: h}
	before, err := x.w.c.GetAttr(ctx, &sandboxfs.GetAttrRequest{Target: target})
	if err != nil {
		return err
	}
	size := int64(before.Attr.Size)
	if !isType(before.Attr, sandboxfs.ModeRegular) || size > exportFileBytes || size > exportBatchBytes-x.bytes {
		return errors.New("workspace export exceeds file bound")
	}
	x.bytes += size
	if err := x.archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: size, Format: tar.FormatPAX}); err != nil {
		return err
	}
	n, err := x.w.read(ctx, h, size, x.archive)
	if err != nil {
		return err
	}
	after, err := x.w.c.GetAttr(ctx, &sandboxfs.GetAttrRequest{Target: target})
	if err != nil {
		return err
	}
	if n != size || after.Attr.Size != before.Attr.Size || after.Attr.Mtime != before.Attr.Mtime {
		return errors.New("workspace output changed during export")
	}
	return nil
}

// marker reports whether the completion marker name in dir holds body.
func (w *world) marker(ctx context.Context, dir sandboxfs.NodeRef, name string, body []byte) (bool, error) {
	data, _, err := w.readFile(ctx, dir, name, markerBytes)
	switch {
	case isErrno(err, sandboxfs.ErrnoNotFound):
		return false, nil
	case err != nil || !bytes.Equal(data, body):
		return false, agentcapabilities.ErrInvalid
	}
	return true, nil
}

// toolEnvironment reads the frozen tool environment in dir; fs.ErrNotExist
// means no step froze it.
func (w *world) toolEnvironment(ctx context.Context, dir sandboxfs.NodeRef) (map[string]string, error) {
	data, _, err := w.readFile(ctx, dir, toolEnvironmentName, toolEnvironmentBytes)
	if isErrno(err, sandboxfs.ErrnoNotFound) {
		return nil, fs.ErrNotExist
	}
	var values map[string]string
	if err != nil || json.Unmarshal(data, &values) != nil || values == nil || !agentcapabilities.ValidToolEnvironment(values, false) {
		return nil, errors.New("the prepared tool environment is unavailable")
	}
	return values, nil
}

// stage writes tree into the unfinished installation at root.
func (w *world) stage(ctx context.Context, root sandboxfs.NodeRef, tree agentcapabilities.Tree) error {
	for _, name := range []string{agentcapabilities.ManifestName, agentcapabilities.ManifestName + ".tmp"} {
		if _, err := w.lookup(ctx, root, name); !isErrno(err, sandboxfs.ErrnoNotFound) {
			return agentcapabilities.ErrInvalid
		}
	}
	return w.writeTree(ctx, root, tree.Root, tree.Files)
}

// readTreeAt reads the tree name below root, as readTree does.
func (w *world) readTreeAt(ctx context.Context, root sandboxfs.NodeRef, name string, immutable bool) ([]agentbundle.File, error) {
	dir, err := w.directory(ctx, root, name, false)
	if err != nil {
		return nil, err
	}
	return w.readTree(ctx, dir, immutable)
}

// finalize completes the staged installation at root as
// agentcapabilities.Finalize does, capturing each declared directory from
// the sandbox.
func (w *world) finalize(ctx context.Context, root sandboxfs.NodeRef, input agentcapabilities.Input, identity agentcapabilities.Identity) error {
	list := func(name string) ([]fs.DirEntry, error) {
		dir, err := w.directory(ctx, root, name, false)
		if err != nil {
			return nil, err
		}
		entries, err := w.list(ctx, dir, agentbundle.MaxFiles)
		if err != nil || len(entries) > agentbundle.MaxFiles {
			return nil, agentcapabilities.ErrInvalid
		}
		result := make([]fs.DirEntry, len(entries))
		for i, e := range entries {
			result[i] = dirEntry{name: string(e.Name), dir: isType(e.Entry.Attr, sandboxfs.ModeDirectory)}
		}
		return result, nil
	}
	snapshot, err := agentcapabilities.NewSnapshot(input, identity, list, func(name string) ([]agentbundle.File, error) { return w.readTreeAt(ctx, root, name, true) })
	if err != nil {
		return err
	}
	for _, source := range input.Directories {
		if agentcapabilities.ValidateLocalDirectories([]string{source}) != nil || overlaps(source, agentcapabilities.Directory) {
			return agentcapabilities.ErrInvalid
		}
		files, err := w.readTreeAt(ctx, w.root, source, false)
		if err != nil {
			return agentcapabilities.ErrInvalid
		}
		tree, err := snapshot.Capture(files)
		if err != nil {
			return err
		}
		if err := w.writeTree(ctx, root, tree.Root, tree.Files); err != nil {
			return err
		}
	}
	body, err := snapshot.Manifest()
	if err != nil {
		return err
	}
	temporary := agentcapabilities.ManifestName + ".tmp"
	if _, err := w.create(ctx, root, temporary, 0o400, body); err != nil {
		return err
	}
	if err := w.rename(ctx, root, temporary, root, agentcapabilities.ManifestName); err != nil {
		return err
	}
	return w.syncDir(ctx, root)
}

// dirEntry is the name and kind of a listed entry, all that
// agentcapabilities.NewSnapshot reads.
type dirEntry struct {
	name string
	dir  bool
}

func (e dirEntry) Name() string { return e.name }
func (e dirEntry) IsDir() bool  { return e.dir }
func (e dirEntry) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e dirEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrInvalid }
