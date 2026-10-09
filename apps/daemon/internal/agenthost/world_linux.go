//go:build linux

package agenthost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

var (
	// errUncertain is a mutation of the sandbox whose outcome is unknown, or
	// one refused because an earlier one's is: the owner never replays it.
	errUncertain = errors.New("agenthost: an Environment mutation's outcome is unknown")
	// errNotDirectory is a path component that is missing, not a directory or
	// a symbolic link, which is never followed.
	errNotDirectory = errors.New("agenthost: not a directory")
	errTooLarge     = errors.New("agenthost: file exceeds its bound")
)

// world is the owner's File client on the sandbox's world export. The owner
// runs one operation on it at a time and forgets, as each ends, every node
// reference it acquired except the root's.
type world struct {
	c       *sandboxfs.Client
	caps    sandboxfs.Capabilities
	root    sandboxfs.NodeRef
	handles *sandboxfs.HandleIDs
	refs    map[sandboxfs.NodeRef]uint64
	// uncertain is the owner's quarantine. A mutation whose effect is
	// possible sets it, and no mutation is sent while it holds.
	uncertain *bool
}

// attachWorld describes the File service on stream, checks that it serves
// what the owner needs, and attaches the world export.
func attachWorld(ctx context.Context, stream io.ReadWriteCloser, uncertain *bool) (*world, error) {
	w := &world{c: sandboxfs.NewClient(stream), handles: new(sandboxfs.HandleIDs), refs: map[sandboxfs.NodeRef]uint64{}, uncertain: uncertain}
	d, err := w.c.Describe(ctx, &sandboxfs.DescribeRequest{})
	if err != nil {
		w.c.Close()
		return nil, err
	}
	caps := d.Capabilities
	if caps.ReadOnly || !caps.HardLinks || !caps.AtomicRename || !caps.DirectoryFsync || !caps.ReadDirPlus || caps.MaxReadBytes == 0 ||
		caps.MaxWriteBytes == 0 || caps.MaxReadDirBytes == 0 || !slices.Contains(d.Exports, sandboxfs.WorldExport) {
		w.c.Close()
		return nil, errors.New("agenthost: the File service does not serve a writable world")
	}
	if caps.MaxTreeEntries < agentbundle.MaxFiles || caps.MaxTreeDataBytes < agentbundle.MaxExpandedBytes {
		w.c.Close()
		return nil, sandboxfs.NewFailure(sandboxfs.CodeUnsupported, sandboxwire.EffectNone, "File service does not support bounded Environment trees")
	}
	w.caps = caps
	a, err := w.c.Attach(ctx, &sandboxfs.AttachRequest{Export: sandboxfs.WorldExport})
	if err != nil {
		w.c.Close()
		return nil, err
	}
	w.root = a.Root.Node
	return w, nil
}

// ended reports whether the stream has failed or closed.
func (w *world) ended() bool {
	select {
	case <-w.c.Done():
		return true
	default:
		return false
	}
}

func (w *world) track(e sandboxfs.Entry) sandboxfs.Entry {
	w.refs[e.Node]++
	return e
}

// forget drops the references the operation acquired.
func (w *world) forget(ctx context.Context) error {
	var batch []sandboxfs.ForgetEntry
	for node, count := range w.refs {
		batch = append(batch, sandboxfs.ForgetEntry{Node: node, Count: count})
	}
	clear(w.refs)
	for len(batch) > 0 {
		n := min(len(batch), 4096)
		if _, err := w.c.Forget(ctx, &sandboxfs.ForgetRequest{Entries: batch[:n]}); err != nil {
			return err
		}
		batch = batch[n:]
	}
	return nil
}

// mutation returns err, the failure of a request that may change the world.
// A failure whose effect is possible quarantines the owner.
func (w *world) mutation(err error) error {
	var f *sandboxfs.Failure
	if err != nil && (!errors.As(err, &f) || f.Effect != sandboxwire.EffectNone) {
		*w.uncertain = true
		return fmt.Errorf("%w: %w", errUncertain, err)
	}
	return err
}

// mutable refuses a mutation while the owner is quarantined.
func (w *world) mutable() error {
	if *w.uncertain {
		return errUncertain
	}
	return nil
}

func isErrno(err error, errno sandboxfs.Errno) bool {
	var f *sandboxfs.Failure
	return errors.As(err, &f) && f.Code == sandboxfs.CodeErrno && f.Errno == errno
}

func isType(a sandboxfs.Attr, t uint32) bool { return a.Mode&sandboxfs.ModeType == t }

// components splits a path below a directory into its names; "", "." and
// "/" name the directory itself.
func components(p string) ([]string, error) {
	if p = strings.Trim(p, "/"); p == "" || p == "." {
		return nil, nil
	}
	if !proto.ValidWorkspacePath(p) {
		return nil, fs.ErrInvalid
	}
	return strings.Split(p, "/"), nil
}

func (w *world) lookup(ctx context.Context, dir sandboxfs.NodeRef, name string) (sandboxfs.Entry, error) {
	r, err := w.c.Lookup(ctx, &sandboxfs.LookupRequest{Parent: dir, Name: []byte(name)})
	if err != nil {
		return sandboxfs.Entry{}, err
	}
	return w.track(r.Entry), nil
}

// directory resolves p below dir: each component must be a directory, and
// none a symbolic link. With create, it makes a missing one with mode 0700.
func (w *world) directory(ctx context.Context, dir sandboxfs.NodeRef, p string, create bool) (sandboxfs.NodeRef, error) {
	names, err := components(p)
	if err != nil {
		return sandboxfs.NodeRef{}, err
	}
	for _, name := range names {
		e, err := w.lookup(ctx, dir, name)
		if create && isErrno(err, sandboxfs.ErrnoNotFound) {
			if e, err = w.mkdir(ctx, dir, name); isErrno(err, sandboxfs.ErrnoExists) {
				e, err = w.lookup(ctx, dir, name)
			}
		}
		switch {
		case isErrno(err, sandboxfs.ErrnoNotFound) || isErrno(err, sandboxfs.ErrnoNotDirectory):
			return sandboxfs.NodeRef{}, errNotDirectory
		case err != nil:
			return sandboxfs.NodeRef{}, err
		case !isType(e.Attr, sandboxfs.ModeDirectory):
			return sandboxfs.NodeRef{}, errNotDirectory
		}
		dir = e.Node
	}
	return dir, nil
}

func (w *world) mkdir(ctx context.Context, dir sandboxfs.NodeRef, name string) (sandboxfs.Entry, error) {
	if err := w.mutable(); err != nil {
		return sandboxfs.Entry{}, err
	}
	r, err := w.c.Mkdir(ctx, &sandboxfs.MkdirRequest{Parent: dir, Name: []byte(name), Mode: 0o700})
	if err != nil {
		return sandboxfs.Entry{}, w.mutation(err)
	}
	return w.track(r.Entry), nil
}

// open returns the regular file's attempted handle even when its outcome is unknown.
func (w *world) open(ctx context.Context, e sandboxfs.Entry) (sandboxfs.HandleID, error) {
	if !isType(e.Attr, sandboxfs.ModeRegular) {
		return 0, fs.ErrInvalid
	}
	h := w.handles.Next()
	if _, err := w.c.Open(ctx, &sandboxfs.OpenRequest{Handle: h, Node: e.Node, Access: sandboxfs.AccessRead, Flags: sandboxfs.OpenNoFollow}); err != nil {
		return h, err
	}
	return h, nil
}

// read reads the open file h from its start to its end, at most limit
// bytes, into out.
func (w *world) read(ctx context.Context, h sandboxfs.HandleID, limit int64, out io.Writer) (int64, error) {
	var n int64
	for {
		r, err := w.c.Read(ctx, &sandboxfs.ReadRequest{Handle: h, Offset: uint64(n), Size: w.caps.MaxReadBytes})
		if err != nil {
			return n, err
		}
		if n += int64(len(r.Data)); n > limit {
			return n, errTooLarge
		}
		if _, err := out.Write(r.Data); err != nil {
			return n, err
		}
		if len(r.Data) < int(w.caps.MaxReadBytes) {
			return n, nil
		}
	}
}

func (w *world) release(ctx context.Context, h sandboxfs.HandleID) {
	w.c.Release(ctx, &sandboxfs.ReleaseRequest{Handle: h})
}

// readFile reads the regular file name in dir, at most limit bytes.
func (w *world) readFile(ctx context.Context, dir sandboxfs.NodeRef, name string, limit int64) ([]byte, sandboxfs.Attr, error) {
	e, err := w.lookup(ctx, dir, name)
	if err != nil {
		return nil, sandboxfs.Attr{}, err
	}
	body, err := w.readEntry(ctx, e, limit)
	return body, e.Attr, err
}

// readEntry reads the referenced regular file, even if its name changes after lookup.
func (w *world) readEntry(ctx context.Context, e sandboxfs.Entry, limit int64) ([]byte, error) {
	if !isType(e.Attr, sandboxfs.ModeRegular) || int64(e.Attr.Size) > limit {
		return nil, fs.ErrInvalid
	}
	h, err := w.open(ctx, e)
	var failure *sandboxfs.Failure
	if errors.As(err, &failure) && failure.Effect == sandboxwire.EffectNone {
		return nil, err
	}
	b := bytes.NewBuffer([]byte{})
	if err == nil {
		var n int64
		n, err = w.read(ctx, h, limit, b)
		if err == nil && uint64(n) != e.Attr.Size {
			err = fs.ErrInvalid
		}
	}
	// Release also joins an Open whose reply was lost to cancellation. The
	// server orders release after acquisition of this handle.
	return b.Bytes(), errors.Join(err, w.closeHandle(ctx, h, false))
}

// closeHandle settles an acquired or uncertain handle despite caller cancellation.
func (w *world) closeHandle(ctx context.Context, h sandboxfs.HandleID, directory bool) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeBound)
	defer cancel()
	var err error
	if directory {
		_, err = w.c.ReleaseDir(cleanup, &sandboxfs.ReleaseDirRequest{Handle: h})
	} else {
		_, err = w.c.Release(cleanup, &sandboxfs.ReleaseRequest{Handle: h})
	}
	if err != nil {
		w.c.Close()
	}
	return err
}

// create makes name in dir, exclusively, with mode, writes data to it and
// syncs it.
func (w *world) create(ctx context.Context, dir sandboxfs.NodeRef, name string, mode uint32, data []byte) (sandboxfs.Entry, error) {
	if err := w.mutable(); err != nil {
		return sandboxfs.Entry{}, err
	}
	e, h, err := w.createFile(ctx, dir, name, mode, data)
	if e.Node.ID != 0 {
		w.track(e)
	}
	err = w.mutation(err)
	if h != 0 {
		err = errors.Join(err, w.mutation(w.closeHandle(ctx, h, false)))
	}
	return e, err
}

// createFile returns its acquired or uncertain handle to the caller for release.
// It does not change the owner's references or quarantine.
func (w *world) createFile(ctx context.Context, dir sandboxfs.NodeRef, name string, mode uint32, data []byte) (sandboxfs.Entry, sandboxfs.HandleID, error) {
	h := w.handles.Next()
	r, err := w.c.Create(ctx, &sandboxfs.CreateRequest{Handle: h, Parent: dir, Name: []byte(name), Mode: mode, Access: sandboxfs.AccessWrite, Exclusive: true})
	if err != nil {
		var f *sandboxfs.Failure
		if errors.As(err, &f) && f.Effect == sandboxwire.EffectNone {
			h = 0
		}
		return sandboxfs.Entry{}, h, err
	}
	e := r.Entry
	for off := 0; off < len(data); {
		chunk := data[off:min(len(data), off+int(w.caps.MaxWriteBytes))]
		r, err := w.c.Write(ctx, &sandboxfs.WriteRequest{Handle: h, Offset: uint64(off), Data: chunk})
		if err == nil && r.Failure != nil {
			err = r.Failure
		}
		if err == nil && r.Written == 0 {
			err = io.ErrShortWrite
		}
		if err != nil {
			return e, h, err
		}
		off += int(r.Written)
	}
	_, err = w.c.Fsync(ctx, &sandboxfs.FsyncRequest{Handle: h})
	return e, h, err
}

func (w *world) link(ctx context.Context, node, dir sandboxfs.NodeRef, name string) error {
	if err := w.mutable(); err != nil {
		return err
	}
	r, err := w.c.Link(ctx, &sandboxfs.LinkRequest{Node: node, NewParent: dir, NewName: []byte(name)})
	if err != nil {
		return w.mutation(err)
	}
	w.track(r.Entry)
	return nil
}

func (w *world) rename(ctx context.Context, dir sandboxfs.NodeRef, name string, newDir sandboxfs.NodeRef, newName string) error {
	if err := w.mutable(); err != nil {
		return err
	}
	_, err := w.c.Rename(ctx, &sandboxfs.RenameRequest{Parent: dir, Name: []byte(name), NewParent: newDir, NewName: []byte(newName), Mode: sandboxfs.RenameReplace})
	return w.mutation(err)
}

// remove unlinks a temporary file of the owner's. It never quarantines: the
// file was never published.
func (w *world) remove(ctx context.Context, dir sandboxfs.NodeRef, name string) {
	w.c.Unlink(ctx, &sandboxfs.UnlinkRequest{Parent: dir, Name: []byte(name)})
}

func (w *world) syncDir(ctx context.Context, dir sandboxfs.NodeRef) error {
	h, err := w.syncDirectory(ctx, dir)
	err = w.mutation(err)
	if h != 0 {
		err = errors.Join(err, w.mutation(w.closeHandle(ctx, h, true)))
	}
	return err
}

// syncDirectory leaves handle release and quarantine with its caller.
func (w *world) syncDirectory(ctx context.Context, dir sandboxfs.NodeRef) (sandboxfs.HandleID, error) {
	h := w.handles.Next()
	if _, err := w.c.OpenDir(ctx, &sandboxfs.OpenDirRequest{Handle: h, Node: dir}); err != nil {
		var f *sandboxfs.Failure
		if errors.As(err, &f) && f.Effect == sandboxwire.EffectNone {
			h = 0
		}
		return h, err
	}
	_, err := w.c.Fsync(ctx, &sandboxfs.FsyncRequest{Handle: h})
	return h, err
}

// publish writes data as name in dir with mode through a temporary file, so
// name holds complete bytes or nothing. With replace it replaces an existing
// name; without, an existing name fails with fs.ErrExist.
func (w *world) publish(ctx context.Context, dir sandboxfs.NodeRef, name string, mode uint32, data []byte, replace bool) error {
	temporary := ".oac-" + uuid.NewString()
	e, err := w.create(ctx, dir, temporary, mode, data)
	switch {
	case err != nil:
	case replace:
		err = w.rename(ctx, dir, temporary, dir, name)
	default:
		// The temporary name stays after a link, and after a link that failed.
		if err = w.link(ctx, e.Node, dir, name); !errors.Is(err, errUncertain) {
			w.remove(ctx, dir, temporary)
			if isErrno(err, sandboxfs.ErrnoExists) {
				return fs.ErrExist
			}
			if err == nil {
				return w.syncDir(ctx, dir)
			}
			return err
		}
	}
	if err != nil {
		if !errors.Is(err, errUncertain) {
			w.remove(ctx, dir, temporary)
		}
		return err
	}
	return w.syncDir(ctx, dir)
}

// list reads dir's entries with their attributes, in the directory's order,
// until it has more than limit or the directory ends.
func (w *world) list(ctx context.Context, dir sandboxfs.NodeRef, limit int) ([]sandboxfs.DirEntry, error) {
	h := w.handles.Next()
	if _, err := w.c.OpenDir(ctx, &sandboxfs.OpenDirRequest{Handle: h, Node: dir}); err != nil {
		return nil, err
	}
	defer w.c.ReleaseDir(ctx, &sandboxfs.ReleaseDirRequest{Handle: h})
	var entries []sandboxfs.DirEntry
	var cookie uint64
	for len(entries) <= limit {
		r, err := w.c.ReadDir(ctx, &sandboxfs.ReadDirRequest{Handle: h, Cookie: cookie, Limit: w.caps.MaxReadDirBytes, WithAttrs: true})
		if err != nil {
			return nil, err
		}
		for _, e := range r.Entries {
			if e.Entry == nil {
				return nil, fs.ErrInvalid
			}
			w.track(*e.Entry)
			entries = append(entries, e)
			cookie = e.Cookie
		}
		if r.End || len(r.Entries) == 0 {
			break
		}
	}
	return entries, nil
}

// sorted lists dir as list does and sorts the entries by name.
func (w *world) sorted(ctx context.Context, dir sandboxfs.NodeRef, limit int) ([]sandboxfs.DirEntry, error) {
	entries, err := w.list(ctx, dir, limit)
	slices.SortFunc(entries, func(a, b sandboxfs.DirEntry) int { return strings.Compare(string(a.Name), string(b.Name)) })
	return entries, err
}

// readTree reads a bounded tree through one owned File result handle. The File
// protocol validates its structure; capability interpretation stays with the owner.
func (w *world) readTree(ctx context.Context, dir sandboxfs.NodeRef, immutable bool) (files []agentbundle.File, err error) {
	q := sandboxfs.OpenTreeRequest{Handle: w.handles.Next(), Node: dir, MaxEntries: agentbundle.MaxFiles,
		MaxDataBytes: agentbundle.MaxExpandedBytes, RequireReadOnlyFiles: immutable}
	result, err := w.c.OpenTree(ctx, &q)
	var failure *sandboxfs.Failure
	if errors.As(err, &failure) && failure.Effect == sandboxwire.EffectNone {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, w.closeHandle(ctx, q.Handle, false))
		if err != nil {
			files = nil
		}
	}()
	if err != nil {
		return nil, err
	}
	reader := &treeResultReader{ctx: ctx, w: w, handle: q.Handle, remaining: result.Size}
	decoder, err := sandboxfs.NewTreeDecoder(reader, q, w.caps, result.Size)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	files = []agentbundle.File{}
	for {
		record, body, readErr := decoder.Next()
		if readErr == io.EOF {
			return files, nil
		}
		if readErr != nil {
			return nil, readErr
		}
		name := string(record.Name)
		if len(paths) != 0 && paths[record.Parent] != "" {
			name = paths[record.Parent] + "/" + name
		}
		paths = append(paths, name)
		if !isType(record.Attr, sandboxfs.ModeRegular) {
			continue
		}
		data := make([]byte, int(record.Attr.Size))
		if _, err := io.ReadFull(body, data); err != nil {
			return nil, err
		}
		files = append(files, agentbundle.File{Path: name, Data: data, Executable: record.Attr.Mode&0o111 != 0})
	}
}

// treeResultReader fetches bounded chunks while the protocol decoder consumes
// records directly into their final file buffers. It never buffers the whole tree.
type treeResultReader struct {
	ctx               context.Context
	w                 *world
	handle            sandboxfs.HandleID
	remaining, offset uint64
	buffer            []byte
}

func (r *treeResultReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if len(r.buffer) == 0 {
		if r.remaining == 0 {
			return 0, io.EOF
		}
		size := uint32(min(uint64(r.w.caps.MaxReadBytes), r.remaining))
		reply, err := r.w.c.Read(r.ctx, &sandboxfs.ReadRequest{Handle: r.handle, Offset: r.offset, Size: size})
		if err != nil {
			return 0, err
		}
		if len(reply.Data) != int(size) {
			return 0, io.ErrUnexpectedEOF
		}
		r.buffer = reply.Data
		r.remaining -= uint64(size)
		r.offset += uint64(size)
	}
	n := copy(dst, r.buffer)
	r.buffer = r.buffer[n:]
	return n, nil
}

// writeTree creates name below dir, which must not exist, with files: its
// directories 0700, and each file 0400, or 0500 when executable. Every
// directory it made is synced.
func (w *world) writeTree(ctx context.Context, dir sandboxfs.NodeRef, name string, files []agentbundle.File) error {
	names, err := components(name)
	if err != nil || len(names) == 0 {
		return fs.ErrInvalid
	}
	parent, err := w.directory(ctx, dir, strings.Join(names[:len(names)-1], "/"), true)
	if err != nil {
		return err
	}
	e, err := w.mkdir(ctx, parent, names[len(names)-1])
	if err != nil {
		return err
	}
	dirs := map[string]sandboxfs.NodeRef{"": e.Node}
	order := []string{""}
	parents := make([]sandboxfs.NodeRef, len(files))
	for i, file := range files {
		parts, err := components(file.Path)
		if err != nil || len(parts) == 0 {
			return fs.ErrInvalid
		}
		at := ""
		for _, part := range parts[:len(parts)-1] {
			next := strings.TrimPrefix(at+"/"+part, "/")
			if _, ok := dirs[next]; !ok {
				d, err := w.mkdir(ctx, dirs[at], part)
				if err != nil {
					return err
				}
				dirs[next] = d.Node
				order = append(order, next)
			}
			at = next
		}
		parents[i] = dirs[at]
	}
	// Each phase admits at most the declared number of handles. A failed
	// task stops new admissions; already admitted work settles before the
	// owner tracks every acquired reference and classifies every failure.
	run := func(n int, directory bool, work func(int) (sandboxfs.Entry, sandboxfs.HandleID, error)) error {
		results := make([]struct {
			entry       sandboxfs.Entry
			err, closed error
		}, n)
		var jobs errgroup.Group
		jobs.SetLimit(int(min(uint32(4), w.caps.MaxOpenHandles)))
		var stopped atomic.Bool
		for i := range n {
			jobs.Go(func() error {
				if stopped.Load() {
					return nil
				}
				var h sandboxfs.HandleID
				results[i].entry, h, results[i].err = work(i)
				if results[i].err != nil {
					stopped.Store(true)
				}
				if h != 0 {
					results[i].closed = w.closeHandle(ctx, h, directory)
					if results[i].closed != nil {
						stopped.Store(true)
					}
				}
				return nil
			})
		}
		jobs.Wait()
		var errs []error
		for _, r := range results {
			if r.entry.Node.ID != 0 {
				w.track(r.entry)
			}
			errs = append(errs, w.mutation(r.err), w.mutation(r.closed))
		}
		return errors.Join(errs...)
	}
	if err := run(len(files), false, func(i int) (sandboxfs.Entry, sandboxfs.HandleID, error) {
		file := files[i]
		mode := uint32(0o400)
		if file.Executable {
			mode = 0o500
		}
		return w.createFile(ctx, parents[i], path.Base(file.Path), mode, file.Data)
	}); err != nil {
		return err
	}
	syncs := make([]sandboxfs.NodeRef, 0, len(order)+1)
	for _, d := range order {
		syncs = append(syncs, dirs[d])
	}
	syncs = append(syncs, parent)
	return run(len(syncs), true, func(i int) (sandboxfs.Entry, sandboxfs.HandleID, error) {
		h, err := w.syncDirectory(ctx, syncs[i])
		return sandboxfs.Entry{}, h, err
	})
}
