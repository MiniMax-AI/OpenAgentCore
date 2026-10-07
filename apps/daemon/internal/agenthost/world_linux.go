//go:build linux

package agenthost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/google/uuid"
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

// validPath reports whether p is a relative path as the guest accepts a
// workspace or installation path: plain names without a backslash, NUL, CR
// or LF.
func validPath(p string) bool {
	return p != "." && len(p) <= 4096 && fs.ValidPath(p) && !strings.ContainsAny(p, "\\\x00\r\n")
}

// components splits a path below a directory into its names; "", "." and
// "/" name the directory itself.
func components(p string) ([]string, error) {
	if p = strings.Trim(p, "/"); p == "" || p == "." {
		return nil, nil
	}
	if !validPath(p) {
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

// open opens the regular file e for reading and returns its handle.
func (w *world) open(ctx context.Context, e sandboxfs.Entry) (sandboxfs.HandleID, error) {
	if !isType(e.Attr, sandboxfs.ModeRegular) {
		return 0, fs.ErrInvalid
	}
	h := w.handles.Next()
	if _, err := w.c.Open(ctx, &sandboxfs.OpenRequest{Handle: h, Node: e.Node, Access: sandboxfs.AccessRead, Flags: sandboxfs.OpenNoFollow}); err != nil {
		return 0, err
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
	if !isType(e.Attr, sandboxfs.ModeRegular) || int64(e.Attr.Size) > limit {
		return nil, e.Attr, fs.ErrInvalid
	}
	h, err := w.open(ctx, e)
	if err != nil {
		return nil, e.Attr, err
	}
	defer w.release(ctx, h)
	var b strings.Builder
	n, err := w.read(ctx, h, limit, &b)
	if err == nil && uint64(n) != e.Attr.Size {
		err = fs.ErrInvalid
	}
	return []byte(b.String()), e.Attr, err
}

// create makes name in dir, exclusively, with mode, writes data to it and
// syncs it.
func (w *world) create(ctx context.Context, dir sandboxfs.NodeRef, name string, mode uint32, data []byte) (sandboxfs.Entry, error) {
	if err := w.mutable(); err != nil {
		return sandboxfs.Entry{}, err
	}
	h := w.handles.Next()
	r, err := w.c.Create(ctx, &sandboxfs.CreateRequest{Handle: h, Parent: dir, Name: []byte(name), Mode: mode, Access: sandboxfs.AccessWrite, Exclusive: true})
	if err != nil {
		return sandboxfs.Entry{}, w.mutation(err)
	}
	e := w.track(r.Entry)
	defer w.release(ctx, h)
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
			return e, w.mutation(err)
		}
		off += int(r.Written)
	}
	if _, err := w.c.Fsync(ctx, &sandboxfs.FsyncRequest{Handle: h}); err != nil {
		return e, w.mutation(err)
	}
	return e, nil
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
	h := w.handles.Next()
	if _, err := w.c.OpenDir(ctx, &sandboxfs.OpenDirRequest{Handle: h, Node: dir}); err != nil {
		return err
	}
	defer w.c.ReleaseDir(ctx, &sandboxfs.ReleaseDirRequest{Handle: h})
	_, err := w.c.Fsync(ctx, &sandboxfs.FsyncRequest{Handle: h})
	return w.mutation(err)
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

// readTree reads the tree at dir as agentcapabilities.ReadTree reads a local
// one: at most agentbundle.MaxFiles entries and agentbundle.MaxExpandedBytes,
// regular files and directories only, files without write bits when
// immutable, in fs.WalkDir's order.
func (w *world) readTree(ctx context.Context, dir sandboxfs.NodeRef, immutable bool) ([]agentbundle.File, error) {
	t := treeReader{w: w, immutable: immutable, entries: 1}
	if err := t.walk(ctx, dir, ""); err != nil {
		return nil, err
	}
	return t.files, nil
}

type treeReader struct {
	w         *world
	immutable bool
	files     []agentbundle.File
	entries   int
	total     int
}

func (t *treeReader) walk(ctx context.Context, dir sandboxfs.NodeRef, prefix string) error {
	entries, err := t.w.sorted(ctx, dir, agentbundle.MaxFiles)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if t.entries++; t.entries > agentbundle.MaxFiles {
			return fs.ErrInvalid
		}
		name, attr := prefix+string(e.Name), e.Entry.Attr
		switch {
		case isType(attr, sandboxfs.ModeDirectory):
			if err := t.walk(ctx, e.Entry.Node, name+"/"); err != nil {
				return err
			}
		case !isType(attr, sandboxfs.ModeRegular) || t.immutable && attr.Mode&0o222 != 0 || attr.Size > uint64(agentbundle.MaxExpandedBytes-t.total):
			return fs.ErrInvalid
		default:
			h, err := t.w.open(ctx, *e.Entry)
			if err != nil {
				return err
			}
			var b strings.Builder
			n, err := t.w.read(ctx, h, int64(agentbundle.MaxExpandedBytes-t.total), &b)
			t.w.release(ctx, h)
			if err != nil || uint64(n) != attr.Size {
				return fs.ErrInvalid
			}
			t.total += int(n)
			t.files = append(t.files, agentbundle.File{Path: name, Data: []byte(b.String()), Executable: attr.Mode&0o111 != 0})
		}
	}
	return nil
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
	for _, file := range files {
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
		mode := uint32(0o400)
		if file.Executable {
			mode = 0o500
		}
		if _, err := w.create(ctx, dirs[at], parts[len(parts)-1], mode, file.Data); err != nil {
			return err
		}
	}
	for _, d := range order {
		if err := w.syncDir(ctx, dirs[d]); err != nil {
			return err
		}
	}
	return w.syncDir(ctx, parent)
}
