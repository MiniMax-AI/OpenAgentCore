//go:build linux

package worldfs

import (
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
)

// Operation-local prefetch driven by the kernel scope records of scope_linux.go.
//
// An operation is one eligible syscall instance of one thread, identified by the record's epoch. Its
// entries serve exactly the (parent, name) and node the kernel asks for, once each, and only while the
// kernel still holds the same record: a consumption after the syscall exited is impossible, because the
// exit removed or replaced the record before the thread could run again. Entries never consumed are
// forgotten when the operation is replaced, when the reaper finds its record gone, or when the world stops.

type prefetchKey struct {
	parent sandboxfs.NodeRef
	name   string
}

// prefetched is one walked step: the entry, or the failure that stopped the walk.
type prefetched struct {
	entry sandboxfs.Entry
	err   error
}

type operation struct {
	epoch   uint64
	entries map[prefetchKey]prefetched
	attrs   map[sandboxfs.NodeRef]sandboxfs.Attr
}

const reapEvery = 500 * time.Millisecond

// lookupScoped answers a LOOKUP of (p, name) from the thread's current operation, starting one with a Walk when the thread's syscall record names a plain path whose first component is this request. ok is false when the request takes its ordinary path.
func (f *frontend) lookupScoped(tid uint32, p *inode, name string) (prefetched, bool) {
	if !f.scoped.Load() {
		return prefetched{}, false
	}
	rec, live := lookupScope(f.cgroup, tid)
	f.mu.Lock()
	op := f.operations[tid]
	if op != nil && (!live || op.epoch != rec.epoch) {
		f.endLocked(tid)
		op = nil
	}
	if op != nil {
		pf, ok := f.takeLocked(op, p.ref, name)
		f.mu.Unlock()
		return pf, ok
	}
	f.mu.Unlock()
	if !live || p != f.root {
		return prefetched{}, false
	}
	// Pinned chains are contiguous from the root, so a first component that is not pinned leads to no pinned or synthetic node.
	var names [][]byte
	for i, c := range strings.Split(strings.TrimPrefix(rec.path, "/"), "/") {
		if c == "" || c == "." || c == ".." || uint64(len(c)) > uint64(f.caps.MaxNameBytes) || i == 0 && f.root.fixed[c] != nil || len(names) == int(f.caps.MaxWalkComponents) {
			break
		}
		names = append(names, []byte(c))
	}
	if len(names) == 0 || string(names[0]) != name {
		return prefetched{}, false
	}
	r, err := call(f, f.ctx, (*sandboxfs.Client).Walk, &sandboxfs.WalkRequest{Parent: f.root.ref, Names: names})
	if err != nil {
		// The Walk failed where a Lookup of the first component would have: the same failure answers the request.
		return prefetched{err: err}, true
	}
	op = &operation{epoch: rec.epoch, entries: map[prefetchKey]prefetched{}, attrs: map[sandboxfs.NodeRef]sandboxfs.Attr{}}
	parent := f.root.ref
	for i, e := range r.Entries {
		op.entries[prefetchKey{parent, string(names[i])}] = prefetched{entry: e}
		op.attrs[e.Node] = e.Attr
		parent = e.Node
	}
	if r.Failure != nil && len(r.Entries) < len(names) {
		op.entries[prefetchKey{parent, string(names[len(r.Entries)])}] = prefetched{err: r.Failure}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endLocked(tid)
	if f.closed.Load() || f.dead.Load() {
		f.forgetLocked(op)
		return prefetched{}, false
	}
	// Finished threads can leave entries until the reaper runs. Bound that
	// userspace set independently of the concurrently active kernel records.
	if len(f.operations) >= maxScopes {
		pf, ok := f.takeLocked(op, p.ref, name)
		f.forgetLocked(op)
		return pf, ok
	}
	// The thread is still inside this syscall, since its request is unanswered, so its record is unchanged; the record is checked again anyway before anything later is consumed.
	if f.operations == nil {
		f.operations = map[uint32]*operation{}
	}
	f.operations[tid] = op
	pf, ok := f.takeLocked(op, p.ref, name)
	return pf, ok
}

// attrScoped answers a GETATTR of node n from the thread's current operation while its kernel record is unchanged.
func (f *frontend) attrScoped(tid uint32, n *inode) (sandboxfs.Attr, bool) {
	if !f.scoped.Load() {
		return sandboxfs.Attr{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	op := f.operations[tid]
	if op == nil {
		return sandboxfs.Attr{}, false
	}
	if rec, live := lookupScope(f.cgroup, tid); !live || rec.epoch != op.epoch {
		f.endLocked(tid)
		return sandboxfs.Attr{}, false
	}
	a, ok := op.attrs[n.ref]
	if ok {
		delete(op.attrs, n.ref)
	}
	return a, ok
}

// takeLocked consumes the prefetched step for (parent, name). The caller holds mu.
func (f *frontend) takeLocked(op *operation, parent sandboxfs.NodeRef, name string) (prefetched, bool) {
	k := prefetchKey{parent, name}
	p, ok := op.entries[k]
	if ok {
		delete(op.entries, k)
	}
	return p, ok
}

// endLocked drops the operation of tid and forgets what it still holds. The caller holds mu.
func (f *frontend) endLocked(tid uint32) {
	if op := f.operations[tid]; op != nil {
		delete(f.operations, tid)
		f.forgetLocked(op)
	}
}

func (f *frontend) forgetLocked(op *operation) {
	for _, p := range op.entries {
		if p.err == nil {
			f.unref(p.entry.Node, 1)
		}
	}
}

// reap forgets what operations whose syscall has ended left behind, until the drainer stops. Its period bounds resource retention only; consumption never depends on it.
func (f *frontend) reap() {
	defer close(f.reaped)
	t := time.NewTicker(reapEvery)
	defer t.Stop()
	for {
		select {
		case <-f.drainCtx.Done():
			return
		case <-t.C:
		}
		f.mu.Lock()
		for tid, op := range f.operations {
			if rec, live := lookupScope(f.cgroup, tid); !live || rec.epoch != op.epoch {
				f.endLocked(tid)
			}
		}
		f.mu.Unlock()
	}
}

// endAll ends every operation when the world stops. The caller holds mu.
func (f *frontend) endAll() {
	for tid := range f.operations {
		f.endLocked(tid)
	}
}
