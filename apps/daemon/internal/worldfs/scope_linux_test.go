//go:build linux

package worldfs

import (
	"encoding/binary"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/cilium/ebpf"
	"golang.org/x/sys/unix"
)

func TestScopeLifetimeAndEviction(t *testing.T) {
	if os.Getenv("OAC_TEST_WORLDFS") != "1" {
		t.Skip("requires the worldfs test container")
	}
	ok, err := acquireScopes()
	if !ok || err != nil {
		t.Fatalf("scope programs: %v", err)
	}
	held := true
	defer func() {
		if held {
			releaseScopes()
		}
	}()
	ok, err = acquireScopes()
	if !ok || err != nil {
		t.Fatal(err)
	}
	var ids []ebpf.ProgramID
	for _, p := range scopes.coll.Programs {
		info, err := p.Info()
		if err != nil {
			t.Fatal(err)
		}
		id, ok := info.ID()
		if !ok {
			t.Fatal("no program ID")
		}
		ids = append(ids, id)
	}
	releaseScopes()
	if scopes.refs != 1 || scopes.coll == nil || len(scopes.links) != 3 {
		t.Fatal("first release detached shared programs")
	}
	for _, id := range ids {
		p, err := ebpf.NewProgramFromID(id)
		if err != nil {
			t.Fatal(err)
		}
		p.Close()
	}

	const cg = ^uint64(0)
	const tid = uint32(42)
	var key [scopeKeySize]byte
	binary.LittleEndian.PutUint64(key[:8], cg)
	binary.LittleEndian.PutUint32(key[8:], tid)
	var value [scopeValueSize]byte
	binary.LittleEndian.PutUint64(value[8:], 123)
	m := scopes.coll.Maps["scopes"]
	if err := m.Update(key, value, ebpf.UpdateAny); err != nil {
		t.Fatal(err)
	}
	for i := uint32(100); i < 100+maxScopes*2; i++ {
		k := key
		binary.LittleEndian.PutUint32(k[8:], i)
		if err := m.Update(k, value, ebpf.UpdateAny); err != nil {
			t.Fatal(err)
		}
	}
	if _, live := lookupScope(cg, tid); live {
		t.Fatal("old untouched hint survived two capacities of newer hints")
	}

	// Eviction and epoch replacement must dispose of acquired references instead
	// of answering a later request from the old operation.
	f := New(nil).fs
	defer f.cancel()
	f.scoped.Store(true)
	f.cgroup = cg
	ref := sandboxfs.NodeRef{ID: 99, Generation: 1}
	makeOp := func() *operation {
		return &operation{epoch: 123, entries: map[prefetchKey]prefetched{{parent: ref, name: "child"}: {entry: sandboxfs.Entry{Node: ref}}}, attrs: map[sandboxfs.NodeRef]sandboxfs.Attr{ref: {}}}
	}
	f.operations = map[uint32]*operation{tid: makeOp()}
	if _, ok := f.attrScoped(tid, &inode{ref: ref}); ok {
		t.Fatal("evicted scope returned an attribute")
	}
	if f.forgets[ref] != 1 || len(f.operations) != 0 {
		t.Fatal("evicted operation did not release its unused reference")
	}
	binary.LittleEndian.PutUint64(value[8:], 124)
	if err := m.Update(key, value, ebpf.UpdateAny); err != nil {
		t.Fatal(err)
	}
	f.operations[tid] = makeOp()
	if _, ok := f.attrScoped(tid, &inode{ref: ref}); ok {
		t.Fatal("replacement epoch returned an old attribute")
	}
	if f.forgets[ref] != 2 {
		t.Fatal("replaced operation did not release its unused reference")
	}

	f.operations[tid] = makeOp()
	f.reaped = make(chan struct{})
	go f.reap()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		done := len(f.operations) == 0 && f.forgets[ref] == 3
		f.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reaper retained a finished operation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.stopDrain()
	<-f.reaped
	unregisterView(cg)
	if _, live := lookupScope(cg, tid); live {
		t.Fatal("unregister retained its existing hint")
	}
	// Drop the final reference and verify that the kernel programs disappear.
	releaseScopes()
	held = false
	if scopes.refs != 0 || scopes.coll != nil || len(scopes.links) != 0 {
		t.Fatal("last release retained programs")
	}
	for _, id := range ids {
		p, err := ebpf.NewProgramFromID(id)
		if err == nil {
			p.Close()
			t.Fatalf("program %d still live", id)
		}
		if !errors.Is(err, unix.ENOENT) {
			t.Fatal(err)
		}
	}
	if ok, err := acquireScopes(); !ok || err != nil {
		t.Fatalf("reload: %v", err)
	}
	held = true
}
