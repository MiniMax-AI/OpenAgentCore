//go:build linux

package fileservice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sort"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"golang.org/x/sys/unix"
)

const (
	maxTreeResults      = 4
	maxTreeBackingBytes = 128 << 20
)

// reserveTree holds the backing budget through pending acquisition and the
// final descriptor close. This is independent of the number of open handles.
func (st *state) reserveTree(size uint64) (func(), error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return nil, errStaleAttachment()
	}
	s := st.svc
	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	if st.treeReserved || s.treeCount == maxTreeResults || size > maxTreeBackingBytes-s.treeBytes {
		return nil, sandboxfs.NewFailure(sandboxfs.CodeResourceExhausted, none, "tree result budget is exhausted")
	}
	st.treeReserved = true
	s.treeCount++
	s.treeBytes += size
	st.trees.Add(1)
	return func() {
		st.mu.Lock()
		s.treeMu.Lock()
		s.treeCount--
		s.treeBytes -= size
		st.treeReserved = false
		s.treeMu.Unlock()
		st.mu.Unlock()
		st.trees.Done()
	}, nil
}

// treeCapture holds source objects directly, without publishing lookup
// references. Enumeration finishes and closes its working directory before
// recursion, so depth does not multiply getdents buffers or working fds.
type treeCapture struct {
	st        *state
	ctx       context.Context
	req       *sandboxfs.OpenTreeRequest
	files     []*os.File
	records   []capturedTreeEntry
	count     uint32
	dataBytes uint64
}

type capturedTreeEntry struct {
	record sandboxfs.TreeRecord
	f      *os.File
}

func (t *treeCapture) check() error {
	if err := t.ctx.Err(); err != nil {
		return contextFailure(err, none)
	}
	select {
	case <-t.st.done:
		return errStaleAttachment()
	default:
		return nil
	}
}

func (t *treeCapture) close() {
	for _, f := range t.files {
		f.Close()
	}
	t.files = nil
}

// retain takes ownership of fd even when validation fails.
func (t *treeCapture) retain(fd int, name []byte) (capturedTreeEntry, error) {
	f := os.NewFile(uintptr(fd), "")
	t.files = append(t.files, f)
	var sb unix.Stat_t
	if err := unix.Fstat(fd, &sb); err != nil {
		return capturedTreeEntry{}, err
	}
	e := capturedTreeEntry{record: sandboxfs.TreeRecord{Name: name, Attr: t.st.attr(&sb)}, f: f}
	if t.count >= t.req.MaxEntries {
		return capturedTreeEntry{}, unix.EOVERFLOW
	}
	if err := sandboxfs.ValidateTreeEntry(e.record, t.count == 0, *t.req, t.st.svc.caps); err != nil {
		return capturedTreeEntry{}, treeFailure(err)
	}
	if sb.Mode&sandboxfs.ModeType == sandboxfs.ModeRegular {
		if sb.Size < 0 || uint64(sb.Size) > t.req.MaxDataBytes-t.dataBytes {
			return capturedTreeEntry{}, unix.EOVERFLOW
		}
		t.dataBytes += uint64(sb.Size)
	}
	t.count++
	return e, nil
}

func (t *treeCapture) walk(e capturedTreeEntry, parent uint32) error {
	if err := t.check(); err != nil {
		return err
	}
	index := uint32(len(t.records))
	e.record.Parent = parent
	t.records = append(t.records, e)
	if e.record.Attr.Mode&sandboxfs.ModeType != sandboxfs.ModeDirectory {
		return nil
	}
	children, err := t.children(e)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := t.walk(child, index); err != nil {
			return err
		}
	}
	return nil
}

func (t *treeCapture) children(e capturedTreeEntry) ([]capturedTreeEntry, error) {
	fd, err := t.st.svc.reopen(int(e.f.Fd()), sandboxfs.ModeDirectory, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	c := cursor{buf: make([]byte, 32<<10)}
	children := []capturedTreeEntry{}
	names := map[string]bool{}
	remaining := t.req.MaxEntries - t.count
	for {
		if err := t.check(); err != nil {
			return nil, err
		}
		if len(c.rest) == 0 {
			n, err := eintr(func() (int, error) { return unix.Getdents(fd, c.buf) })
			if err != nil {
				return nil, err
			}
			if n == 0 {
				break
			}
			c.rest = c.buf[:n]
		}
		raw, rest, err := c.next()
		if err != nil {
			return nil, err
		}
		c.rest = rest
		name := string(raw.name)
		if name == "." || name == ".." {
			continue
		}
		if names[name] {
			return nil, unix.EINVAL
		}
		if uint32(len(names)) >= remaining {
			return nil, unix.EOVERFLOW
		}
		names[name] = true
		held, err := openat(fd, name, unix.O_PATH|unix.O_NOFOLLOW, 0)
		if err == unix.ENOENT {
			continue
		}
		if err != nil {
			return nil, err
		}
		child, err := t.retain(held, bytes.Clone(raw.name))
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool { return bytes.Compare(children[i].record.Name, children[j].record.Name) < 0 })
	return children, nil
}

// treeReader checks cancellation at every bounded body read. The encoder
// owns record validation and enforces exact observed file sizes.
type treeReader struct {
	t *treeCapture
	f *os.File
}

func (r treeReader) Read(p []byte) (int, error) {
	if err := r.t.check(); err != nil {
		return 0, err
	}
	return r.f.Read(p)
}

func (s *Service) OpenTree(ctx context.Context, a sandboxfs.Attachment, r *sandboxfs.OpenTreeRequest) (*sandboxfs.OpenTreeResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	n, err := st.dir(r.Node)
	if err != nil {
		return nil, err
	}
	bound, err := sandboxfs.TreeSizeBound(*r, s.caps)
	if err != nil {
		return nil, err
	}
	if err := st.reserve(r.Handle); err != nil {
		return nil, err
	}
	pending := true
	defer func() {
		if pending {
			st.unreserve(r.Handle)
		}
	}()
	releaseBudget, err := st.reserveTree(bound)
	if err != nil {
		return nil, err
	}
	ownedBudget := true
	defer func() {
		if ownedBudget {
			releaseBudget()
		}
	}()
	t := &treeCapture{st: st, ctx: ctx, req: r}
	defer t.close()
	if err := t.check(); err != nil {
		return nil, err
	}
	var root capturedTreeEntry
	err = use(n.f, errStaleNode, func(fd int) error {
		dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			return err
		}
		root, err = t.retain(dup, nil)
		return err
	})
	if err != nil {
		return nil, failure(err, none)
	}
	if err := t.walk(root, 0); err != nil {
		return nil, failure(err, none)
	}
	fd, err := unix.MemfdCreate("oac-file-tree", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, failure(err, none)
	}
	result := os.NewFile(uintptr(fd), "")
	ownedResult := true
	defer func() {
		if ownedResult {
			result.Close()
		}
	}()
	if err := t.encode(result); err != nil {
		return nil, treeFailure(err)
	}
	if err := unix.Fchmod(fd, 0400); err != nil {
		return nil, failure(err, none)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		return nil, failure(err, none)
	}
	var sb unix.Stat_t
	if err := unix.Fstat(fd, &sb); err != nil {
		return nil, failure(err, none)
	}
	t.close()
	if err := t.check(); err != nil {
		return nil, err
	}
	ownedBudget, ownedResult, pending = false, false, false
	if err := st.publish(r.Handle, &handle{f: result, tree: true, afterClose: releaseBudget}); err != nil {
		return nil, failure(err, none)
	}
	return &sandboxfs.OpenTreeResponse{Size: uint64(sb.Size)}, nil
}

// encode reads the objects retained by capture, not their current names.
func (t *treeCapture) encode(w io.Writer) error {
	encoder, err := sandboxfs.NewTreeEncoder(w, *t.req, t.st.svc.caps, t.count, t.dataBytes)
	if err != nil {
		return err
	}
	for _, e := range t.records {
		if err := t.check(); err != nil {
			return err
		}
		var body io.Reader
		var source *os.File
		if e.record.Attr.Mode&sandboxfs.ModeType == sandboxfs.ModeRegular {
			fd, err := t.st.svc.reopen(int(e.f.Fd()), sandboxfs.ModeRegular, unix.O_RDONLY)
			if err != nil {
				return err
			}
			source = os.NewFile(uintptr(fd), "")
			body = treeReader{t: t, f: source}
		}
		err := encoder.Write(e.record, body)
		if source != nil {
			source.Close()
		}
		if err != nil {
			return err
		}
	}
	return encoder.Close()
}

// Malformed captured data is an invalid tree, not a transport or disk error.
func treeFailure(err error) error {
	if errors.Is(err, sandboxwire.ErrMalformed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return failure(unix.EINVAL, none)
	}
	return failure(err, none)
}
