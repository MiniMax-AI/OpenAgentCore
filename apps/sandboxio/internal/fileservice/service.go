//go:build linux

// Package fileservice is the sandbox file service: it implements
// sandboxfs.Service over the one export sandboxfs.WorldExport with the Linux
// *at system calls. It acts as its own process identity and never impersonates.
package fileservice

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"golang.org/x/sys/unix"
)

// Service serves File requests for any number of attachments. Its node and
// handle tables live in memory, so each Service has its own ServerInstanceID.
type Service struct {
	instance sandboxwire.ID
	identity sandboxfs.Identity
	caps     sandboxfs.Capabilities
	root     *os.File // O_PATH directory of the export
	rootDev  uint64
	proc     *os.File // /proc/self/fd, for reopening a held descriptor
	fdinfo   *os.File // /proc/self/fdinfo, for mount IDs statx does not report

	mu     sync.Mutex
	atts   map[sandboxwire.ID]*state
	closed bool
}

// New serves the absolute directory root as the export world. oac-sandbox-io
// passes "/", the sandbox's root after the Provider's namespace setup; the
// caller owns the isolation of everything under root, because the service
// neither detects nor enforces a boundary inside it. New sets the process
// umask to zero, because the service applies every requested mode
// explicitly.
func New(root string) (*Service, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("fileservice: root %q is not absolute", root)
	}
	unix.Umask(0)
	s := &Service{
		instance: sandboxwire.NewID(),
		identity: sandboxfs.Identity{UID: uint32(unix.Geteuid()), GID: uint32(unix.Getegid())},
		atts:     map[sandboxwire.ID]*state{},
	}
	for _, d := range []struct {
		f    **os.File
		path string
	}{{&s.proc, "/proc/self/fd"}, {&s.fdinfo, "/proc/self/fdinfo"}, {&s.root, root}} {
		f, err := openFile(unix.AT_FDCWD, d.path, unix.O_PATH|unix.O_DIRECTORY)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("fileservice: open %s: %w", d.path, err)
		}
		*d.f = f
	}
	var sb unix.Stat_t
	if err := unix.Fstat(int(s.root.Fd()), &sb); err != nil {
		s.Close()
		return nil, fmt.Errorf("fileservice: root %s: %w", root, err)
	}
	s.rootDev = uint64(sb.Dev)
	noReplace, exchange := probeRename()
	s.caps = sandboxfs.Capabilities{
		PathProfile:       sandboxfs.PathProfileLinuxBytes,
		CacheProfile:      sandboxfs.CacheProfileUncached,
		Durability:        sandboxfs.DurabilityFsyncRequired,
		MaxNameBytes:      255,
		MaxPathBytes:      4095,
		MaxReadBytes:      sandboxwire.MaxChunk,
		MaxWriteBytes:     sandboxwire.MaxChunk,
		MaxWalkComponents: 256,
		MaxReadDirBytes:   64 << 10,
		MaxOpenHandles:    4096,
		AtomicAppend:      true,
		AtomicRename:      true,
		RenameNoReplace:   noReplace,
		RenameExchange:    exchange,
		HardLinks:         true,
		Symlinks:          true,
		SetMode:           true,
		SetOwner:          true,
		SetTimes:          true,
		DirectoryFsync:    true,
		ReadDirPlus:       true,
		Flock:             true,
	}
	return s, nil
}

// probeRename reports which renameat2 modes the kernel supports, by trying
// them in a temporary directory.
func probeRename() (noReplace, exchange bool) {
	dir, err := os.MkdirTemp("", "oac-fileservice-")
	if err != nil {
		return false, false
	}
	defer os.RemoveAll(dir)
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if os.WriteFile(a, nil, 0o600) != nil || os.WriteFile(b, nil, 0o600) != nil {
		return false, false
	}
	noReplace = unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_NOREPLACE) == unix.EEXIST
	exchange = unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE) == nil
	return noReplace, exchange
}

// InstanceID is the service incarnation. Link binds each stream to it, and
// the Attachment each stream's sandboxfs.Server.Serve receives carries it.
func (s *Service) InstanceID() sandboxwire.ID { return s.instance }

// Close releases every attachment and the export root. Requests still
// running fail as stale.
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	atts := s.atts
	s.atts = map[sandboxwire.ID]*state{}
	s.mu.Unlock()
	for _, st := range atts {
		st.close()
	}
	for _, f := range []*os.File{s.root, s.proc, s.fdinfo} {
		if f != nil {
			f.Close()
		}
	}
	return nil
}

var (
	errStaleAttachment = func() error {
		return sandboxfs.NewFailure(sandboxfs.CodeStaleAttachment, sandboxwire.EffectNone, "attachment is not attached")
	}
	errStaleNode = func() error {
		return sandboxfs.NewFailure(sandboxfs.CodeStaleNode, sandboxwire.EffectNone, "node is not held by this attachment")
	}
	errStaleHandle = func() error {
		return sandboxfs.NewFailure(sandboxfs.CodeStaleHandle, sandboxwire.EffectNone, "handle is not open in this attachment")
	}
)

// check admits a request for attachment a without requiring it to be attached.
func (s *Service) check(a sandboxfs.Attachment) error {
	if a.ServerInstanceID != s.instance {
		return sandboxfs.NewFailure(sandboxfs.CodeInstanceChanged, sandboxwire.EffectNone, "stream is bound to another service instance")
	}
	if a.Lease == nil {
		return errStaleAttachment()
	}
	select {
	case <-a.Lease.Done():
		return errStaleAttachment()
	default:
		return nil
	}
}

// enter admits r for an attached attachment and returns its state.
func (s *Service) enter(a sandboxfs.Attachment, r sandboxfs.Request) (*state, error) {
	if err := s.check(a); err != nil {
		return nil, err
	}
	s.mu.Lock()
	st := s.atts[a.ID]
	s.mu.Unlock()
	if st == nil {
		return nil, errStaleAttachment()
	}
	if f := s.caps.Admit(r, st.readOnly); f != nil {
		return nil, f
	}
	return st, nil
}

// Describe lists world when the attachment is granted it.
func (s *Service) Describe(_ context.Context, a sandboxfs.Attachment, _ *sandboxfs.DescribeRequest) (*sandboxfs.DescribeResponse, error) {
	if err := s.check(a); err != nil {
		return nil, err
	}
	var exports []sandboxlink.ExportID
	if _, ok := a.Grant(sandboxfs.WorldExport); ok {
		exports = []sandboxlink.ExportID{sandboxfs.WorldExport}
	}
	return &sandboxfs.DescribeResponse{ServerInstanceID: s.instance, Identity: s.identity, Capabilities: s.caps, Exports: exports}, nil
}

func (s *Service) Attach(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.AttachRequest) (*sandboxfs.AttachResponse, error) {
	if err := s.check(a); err != nil {
		return nil, err
	}
	switch g, ok := a.Grant(r.Export); {
	case !ok:
		return nil, sandboxfs.NewFailure(sandboxfs.CodeUnauthorized, sandboxwire.EffectNone, "export "+string(r.Export)+" is not granted")
	case g.ReadOnly && !r.ReadOnly:
		return nil, sandboxfs.NewFailure(sandboxfs.CodeUnauthorized, sandboxwire.EffectNone, "export "+string(r.Export)+" is granted read-only")
	}
	if f := s.caps.Admit(r, r.ReadOnly); f != nil {
		return nil, f
	}
	if r.Export != sandboxfs.WorldExport {
		return nil, sandboxfs.NewFailure(sandboxfs.CodeInvalidArgument, sandboxwire.EffectNone, "unknown export "+string(r.Export))
	}
	var fd int
	err := use(s.root, errStaleAttachment, func(root int) (err error) {
		fd, err = unix.FcntlInt(uintptr(root), unix.F_DUPFD_CLOEXEC, 0)
		return err
	})
	if err != nil {
		return nil, failure(err, sandboxwire.EffectNone)
	}
	var sb unix.Stat_t
	if err := unix.Fstat(fd, &sb); err != nil {
		unix.Close(fd)
		return nil, failure(err, sandboxwire.EffectNone)
	}
	st := newState(s, r.ReadOnly)
	s.mu.Lock()
	switch {
	case s.closed:
		s.mu.Unlock()
		unix.Close(fd)
		return nil, errStaleAttachment()
	case s.atts[a.ID] != nil:
		s.mu.Unlock()
		unix.Close(fd)
		return nil, sandboxfs.NewFailure(sandboxfs.CodeInvalidArgument, sandboxwire.EffectNone, "attachment is already attached")
	}
	s.atts[a.ID] = st
	s.mu.Unlock()
	go s.watch(a, st)
	root, err := st.addNode(fd, &sb)
	if err != nil {
		s.detach(a.ID, st)
		return nil, failure(err, sandboxwire.EffectNone)
	}
	return &sandboxfs.AttachResponse{Root: sandboxfs.Entry{Node: root.ref, Attr: st.attr(&sb)}}, nil
}

// watch detaches st when its lease ends.
func (s *Service) watch(a sandboxfs.Attachment, st *state) {
	select {
	case <-a.Lease.Done():
		s.detach(a.ID, st)
	case <-st.done:
	}
}

func (s *Service) detach(id sandboxwire.ID, st *state) {
	s.mu.Lock()
	if s.atts[id] == st {
		delete(s.atts, id)
	}
	s.mu.Unlock()
	st.close()
}

func (s *Service) Detach(_ context.Context, a sandboxfs.Attachment, r *sandboxfs.DetachRequest) (*sandboxfs.DetachResponse, error) {
	st, err := s.enter(a, r)
	if err != nil {
		return nil, err
	}
	s.detach(a.ID, st)
	return &sandboxfs.DetachResponse{}, nil
}

// state is one attachment: its node and handle tables.
type state struct {
	svc      *Service
	readOnly bool
	done     chan struct{}

	mu         sync.Mutex
	closed     bool
	nodes      []*node // index ID-1
	free       []int
	inodes     map[inodeKey]*node
	generation uint64
	// handles maps each client-chosen ID to its handle, or to nil while the
	// acquisition that reserved the ID runs.
	handles map[sandboxfs.HandleID]*handle
}

// inodeKey identifies a node. The mount ID keeps the same inode reached
// through two mounts, such as a bind mount and its source, in two nodes.
type inodeKey struct{ mnt, dev, ino uint64 }

// mountID returns the ID of the mount the object of fd is reached through:
// statx reports it from Linux 5.8, and the mnt_id line of
// /proc/self/fdinfo/<fd> before that.
func (s *Service) mountID(fd int) (uint64, error) {
	var stx unix.Statx_t
	if unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &stx) == nil && stx.Mask&unix.STATX_MNT_ID != 0 {
		return stx.Mnt_id, nil
	}
	return s.fdinfoMountID(fd)
}

func (s *Service) fdinfoMountID(fd int) (uint64, error) {
	var info []byte
	err := use(s.fdinfo, errStaleAttachment, func(dir int) error {
		f, err := openat(dir, strconv.Itoa(fd), unix.O_RDONLY, 0)
		if err != nil {
			return err
		}
		defer unix.Close(f)
		buf := make([]byte, 4096)
		for {
			n, err := eintr(func() (int, error) { return unix.Read(f, buf) })
			if n <= 0 || err != nil {
				return err
			}
			info = append(info, buf[:n]...)
		}
	})
	if err != nil {
		return 0, err
	}
	for line := range strings.Lines(string(info)) {
		if v, ok := strings.CutPrefix(line, "mnt_id:"); ok {
			return strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		}
	}
	return 0, errors.New("fdinfo has no mnt_id")
}

// node is a file-system object held by an O_PATH descriptor that never
// follows a symlink.
type node struct {
	ref  sandboxfs.NodeRef
	f    *os.File
	key  inodeKey
	typ  uint32 // sandboxfs mode type bits; an inode never changes type
	refs uint64
}

type handle struct {
	f   *os.File
	dir *cursor // set for directory handles

	writeMu sync.Mutex // holds a write and the O_APPEND mode it sets on f

	lockMu sync.Mutex
	flock  sandboxfs.LockMode // held flock mode; zero when none
}

func newState(s *Service, readOnly bool) *state {
	// A random generation base makes a NodeRef from another attachment or
	// incarnation miss instead of naming a live object.
	return &state{
		svc: s, readOnly: readOnly, done: make(chan struct{}),
		inodes: map[inodeKey]*node{}, handles: map[sandboxfs.HandleID]*handle{},
		generation: rand.Uint64() >> 2,
	}
}

// close releases every node and handle; closing a handle releases its locks.
func (st *state) close() {
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return
	}
	st.closed = true
	nodes, handles := st.nodes, st.handles
	st.nodes, st.inodes, st.handles = nil, nil, nil
	close(st.done)
	st.mu.Unlock()
	for _, n := range nodes {
		if n != nil {
			n.f.Close()
		}
	}
	for _, h := range handles {
		if h != nil {
			h.f.Close()
		}
	}
}

// addNode takes ownership of fd and acquires one reference on its node.
func (st *state) addNode(fd int, sb *unix.Stat_t) (*node, error) {
	mnt, err := st.svc.mountID(fd)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	key := inodeKey{mnt, uint64(sb.Dev), sb.Ino}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		unix.Close(fd)
		return nil, errStaleAttachment()
	}
	if n := st.inodes[key]; n != nil {
		n.refs++
		unix.Close(fd)
		return n, nil
	}
	i := len(st.nodes)
	if k := len(st.free); k > 0 {
		i, st.free = st.free[k-1], st.free[:k-1]
	} else {
		st.nodes = append(st.nodes, nil)
	}
	st.generation++
	n := &node{
		ref: sandboxfs.NodeRef{ID: uint64(i) + 1, Generation: st.generation},
		f:   os.NewFile(uintptr(fd), ""), key: key, typ: sb.Mode & sandboxfs.ModeType, refs: 1,
	}
	st.nodes[i], st.inodes[key] = n, n
	return n, nil
}

func (st *state) node(ref sandboxfs.NodeRef) (*node, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return nil, errStaleAttachment()
	}
	if i := ref.ID - 1; i < uint64(len(st.nodes)) {
		if n := st.nodes[i]; n != nil && n.ref == ref {
			return n, nil
		}
	}
	return nil, errStaleNode()
}

// forget applies every entry or none.
func (st *state) forget(entries []sandboxfs.ForgetEntry) error {
	st.mu.Lock()
	var drop []*node
	defer func() {
		st.mu.Unlock()
		for _, n := range drop {
			n.f.Close()
		}
	}()
	if st.closed {
		return errStaleAttachment()
	}
	for _, e := range entries {
		i := e.Node.ID - 1
		if i >= uint64(len(st.nodes)) || st.nodes[i] == nil || st.nodes[i].ref != e.Node {
			return errStaleNode()
		}
		if e.Count > st.nodes[i].refs {
			return sandboxfs.NewFailure(sandboxfs.CodeInvalidArgument, sandboxwire.EffectNone, "forget exceeds the node's references")
		}
	}
	for _, e := range entries {
		i := e.Node.ID - 1
		n := st.nodes[i]
		if n.refs -= e.Count; n.refs == 0 {
			st.nodes[i] = nil
			st.free = append(st.free, int(i))
			delete(st.inodes, n.key)
			drop = append(drop, n)
		}
	}
	return nil
}

// reserve claims the client-chosen handle ID before an acquisition touches
// the file system, so a duplicate ID or exhaustion fails before anything
// happens. A reserved ID counts toward MaxOpenHandles.
func (st *state) reserve(id sandboxfs.HandleID) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return errStaleAttachment()
	}
	if _, ok := st.handles[id]; ok {
		return sandboxfs.NewFailure(sandboxfs.CodeInvalidArgument, sandboxwire.EffectNone, "handle ID is already in use")
	}
	if len(st.handles) >= int(st.svc.caps.MaxOpenHandles) {
		return sandboxfs.NewFailure(sandboxfs.CodeResourceExhausted, sandboxwire.EffectNone, "too many open handles")
	}
	st.handles[id] = nil
	return nil
}

// unreserve drops the reservation of an acquisition that failed.
func (st *state) unreserve(id sandboxfs.HandleID) {
	st.mu.Lock()
	if !st.closed && st.handles[id] == nil {
		delete(st.handles, id)
	}
	st.mu.Unlock()
}

// publish installs h under its reserved ID.
func (st *state) publish(id sandboxfs.HandleID, h *handle) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		h.f.Close()
		return errStaleAttachment()
	}
	st.handles[id] = h
	return nil
}

func (st *state) handle(id sandboxfs.HandleID) (*handle, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return nil, errStaleAttachment()
	}
	if h := st.handles[id]; h != nil {
		return h, nil
	}
	return nil, errStaleHandle()
}

// release closes handle id if it is a directory handle exactly when dir is
// set.
func (st *state) release(id sandboxfs.HandleID, dir bool) error {
	st.mu.Lock()
	h := st.handles[id]
	switch {
	case st.closed:
		st.mu.Unlock()
		return errStaleAttachment()
	case h == nil:
		st.mu.Unlock()
		return errStaleHandle()
	case (h.dir != nil) != dir:
		st.mu.Unlock()
		return sandboxfs.NewErrnoFailure(sandboxfs.ErrnoBadDescriptor, sandboxwire.EffectNone, "wrong handle kind")
	}
	delete(st.handles, id)
	st.mu.Unlock()
	return h.f.Close()
}

// attr converts a stat. Ino combines the device with the inode number, as
// go-fuse's loopback does, so files on different mounts stay distinct.
func (st *state) attr(sb *unix.Stat_t) sandboxfs.Attr {
	return sandboxfs.Attr{
		Ino:     st.ino(uint64(sb.Dev), sb.Ino),
		Mode:    sb.Mode,
		Nlink:   uint32(sb.Nlink),
		UID:     sb.Uid,
		GID:     sb.Gid,
		Rdev:    uint64(sb.Rdev),
		Size:    uint64(sb.Size),
		Blocks:  uint64(sb.Blocks),
		Blksize: uint32(sb.Blksize),
		Atime:   timestamp(sb.Atim),
		Mtime:   timestamp(sb.Mtim),
		Ctime:   timestamp(sb.Ctim),
	}
}

func (st *state) ino(dev, ino uint64) uint64 {
	swap := func(d uint64) uint64 { return d<<32 | d>>32 }
	return swap(dev) ^ swap(st.svc.rootDev) ^ ino
}

func timestamp(t unix.Timespec) sandboxfs.Timestamp {
	return sandboxfs.Timestamp{Sec: int64(t.Sec), Nsec: uint32(t.Nsec)}
}
