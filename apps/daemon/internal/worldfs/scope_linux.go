//go:build linux

package worldfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/link"
)

// Syscall-local metadata prediction observed with BPF.
//
// One raw-tracepoint program set per daemon process publishes, for threads of registered View cgroups only,
// the pathname of an eligible non-mutating syscall (statx, newfstatat, readlink, readlinkat, openat with
// O_PATH) at sys_enter, keyed by (cgroup, thread ID in the View's PID namespace), with an epoch unique to
// that syscall instance; every sys_enter of the thread first removes its previous record, and sys_exit and
// sched_process_exit remove it synchronously. The kernel runs the real syscall unchanged. A FUSE request
// consults the record of its own thread (FUSE names the thread in the mount's PID namespace): the first
// LOOKUP of a syscall whose record names a plain path runs one Walk inside that blocked syscall, and later
// LOOKUP/GETATTR of the same syscall consume its entries only while the record's epoch is unchanged. Once
// the syscall exited, the record is gone or carries a new epoch, so nothing of a finished operation can be
// consumed; a bounded reaper forgets what finished operations left. No event delivery is trusted.
//
// When the programs cannot be loaded (no capability, LSM, kernel configuration), the world serves every
// request as before: the scope is an acceleration of the same File requests, not a protocol capability.

const (
	scopeKeySize   = 16  // cgroup u64, thread ID u32, pad u32
	scopeValueSize = 280 // counter u64, epoch u64, syscall u64, path [256]byte
	scopePathMax   = 256
	maxScopes      = 4096
	maxViews       = 256
)

type scopeRecord struct {
	epoch uint64
	nr    uint64
	path  string
}

// scopes is the process-wide loader, shared by every world of the daemon and reference counted.
var scopes struct {
	mu    sync.Mutex
	refs  int
	coll  *ebpf.Collection
	links []link.Link
	err   error
}

// acquireScopes loads and attaches the programs for the first world and reports whether scopes are available.
func acquireScopes() (bool, error) {
	if runtime.GOARCH != "amd64" {
		return false, fmt.Errorf("worldfs: syscall scope register layout unavailable on %s", runtime.GOARCH)
	}
	scopes.mu.Lock()
	defer scopes.mu.Unlock()
	if scopes.refs > 0 {
		scopes.refs++
		return true, nil
	}
	if scopes.err != nil {
		return false, scopes.err
	}
	coll, err := ebpf.NewCollection(scopeSpec())
	if err != nil {
		scopes.err = fmt.Errorf("worldfs: load scope programs: %w", err)
		return false, scopes.err
	}
	var links []link.Link
	for prog, tp := range map[string][]string{"scope_enter": {"sys_enter"}, "scope_clear": {"sys_exit", "sched_process_exit"}} {
		for _, name := range tp {
			l, err := link.AttachRawTracepoint(link.RawTracepointOptions{Name: name, Program: coll.Programs[prog]})
			if err != nil {
				for _, l := range links {
					l.Close()
				}
				coll.Close()
				scopes.err = fmt.Errorf("worldfs: attach %s: %w", name, err)
				return false, scopes.err
			}
			links = append(links, l)
		}
	}
	scopes.coll, scopes.links, scopes.refs = coll, links, 1
	return true, nil
}

func releaseScopes() {
	scopes.mu.Lock()
	defer scopes.mu.Unlock()
	if scopes.refs--; scopes.refs > 0 {
		return
	}
	for _, l := range scopes.links {
		l.Close()
	}
	scopes.coll.Close()
	scopes.coll, scopes.links = nil, nil
}

// registerView publishes the View's cgroup with its PID namespace identity. The caller holds a reference.
func registerView(cgroup, nsDev, nsIno uint64) error {
	var v [16]byte
	binary.LittleEndian.PutUint64(v[0:], nsDev)
	binary.LittleEndian.PutUint64(v[8:], nsIno)
	return scopes.coll.Maps["views"].Update(cgroup, v, ebpf.UpdateAny)
}

func unregisterView(cgroup uint64) {
	_ = scopes.coll.Maps["views"].Delete(cgroup)
	// These records are disposable hints, not remote references. An enter
	// already in progress can still publish after this scan; the bounded LRU
	// lets later active scopes replace that hint instead of exhausting capacity.
	m := scopes.coll.Maps["scopes"]
	var key [scopeKeySize]byte
	var value [scopeValueSize]byte
	var remove [][scopeKeySize]byte
	it := m.Iterate()
	for it.Next(&key, &value) {
		if binary.LittleEndian.Uint64(key[:8]) == cgroup {
			remove = append(remove, key)
		}
	}
	for _, key := range remove {
		_ = m.Delete(key)
	}
}

// lookupScope returns the current record of the thread, if the kernel holds one.
func lookupScope(cgroup uint64, tid uint32) (scopeRecord, bool) {
	scopes.mu.Lock()
	defer scopes.mu.Unlock()
	if scopes.coll == nil {
		return scopeRecord{}, false
	}
	var k [scopeKeySize]byte
	binary.LittleEndian.PutUint64(k[0:], cgroup)
	binary.LittleEndian.PutUint32(k[8:], tid)
	var v [scopeValueSize]byte
	if err := scopes.coll.Maps["scopes"].Lookup(k, &v); err != nil {
		if !errors.Is(err, ebpf.ErrKeyNotExist) {
			// Any failure means no scope: the request takes its ordinary path.
			return scopeRecord{}, false
		}
		return scopeRecord{}, false
	}
	path := v[24:]
	n := 0
	for n < len(path) && path[n] != 0 {
		n++
	}
	return scopeRecord{epoch: binary.LittleEndian.Uint64(v[8:]), nr: binary.LittleEndian.Uint64(v[16:]), path: string(path[:n])}, true
}

// scopeSpec is the program set. Register use: R6 ctx, R7 syscall number, R8 scratch record, R9 regs then user pointer. Stack: fp-16 key (cgroup, tid, pad), fp-24 pidns info, fp-32 scratch word, fp-40 array index. amd64 pt_regs offsets: rdx 96, rsi 104, rdi 112.
func scopeSpec() *ebpf.CollectionSpec {
	prefix := func() asm.Instructions {
		return asm.Instructions{
			asm.Mov.Reg(asm.R6, asm.R1),
			asm.FnGetCurrentCgroupId.Call(),
			asm.StoreMem(asm.RFP, -16, asm.R0, asm.DWord),
			asm.LoadMapPtr(asm.R1, 0).WithReference("views"),
			asm.Mov.Reg(asm.R2, asm.RFP), asm.Add.Imm(asm.R2, -16),
			asm.FnMapLookupElem.Call(),
			asm.JEq.Imm(asm.R0, 0, "exit"),
			asm.LoadMem(asm.R1, asm.R0, 0, asm.DWord),
			asm.LoadMem(asm.R2, asm.R0, 8, asm.DWord),
			asm.Mov.Reg(asm.R3, asm.RFP), asm.Add.Imm(asm.R3, -24),
			asm.Mov.Imm(asm.R4, 8),
			asm.FnGetNsCurrentPidTgid.Call(),
			asm.JNE.Imm(asm.R0, 0, "exit"),
			asm.LoadMem(asm.R1, asm.RFP, -24, asm.Word),
			asm.StoreMem(asm.RFP, -8, asm.R1, asm.Word),
			asm.StoreImm(asm.RFP, -4, 0, asm.Word),
			// Every observed syscall entry or exit of the thread first drops its previous record.
			asm.LoadMapPtr(asm.R1, 0).WithReference("scopes"),
			asm.Mov.Reg(asm.R2, asm.RFP), asm.Add.Imm(asm.R2, -16),
			asm.FnMapDeleteElem.Call(),
		}
	}
	exit := asm.Instructions{asm.Mov.Imm(asm.R0, 0).WithSymbol("exit"), asm.Return()}
	enter := append(prefix(), asm.Instructions{
		asm.LoadMem(asm.R7, asm.R6, 8, asm.DWord), // syscall number
		asm.LoadMem(asm.R9, asm.R6, 0, asm.DWord), // struct pt_regs *
		asm.JEq.Imm(asm.R7, 89, "rdi"),            // readlink
		asm.JEq.Imm(asm.R7, 262, "rsi"),           // newfstatat
		asm.JEq.Imm(asm.R7, 267, "rsi"),           // readlinkat
		asm.JEq.Imm(asm.R7, 332, "rsi"),           // statx
		asm.JNE.Imm(asm.R7, 257, "exit"),          // openat: O_PATH only
		asm.Mov.Reg(asm.R1, asm.RFP), asm.Add.Imm(asm.R1, -32), asm.Mov.Imm(asm.R2, 8), asm.Mov.Reg(asm.R3, asm.R9), asm.Add.Imm(asm.R3, 96),
		asm.FnProbeReadKernel.Call(),
		asm.JNE.Imm(asm.R0, 0, "exit"),
		asm.LoadMem(asm.R1, asm.RFP, -32, asm.DWord),
		asm.JSet.Imm(asm.R1, 0x200000, "rsi"),
		asm.Ja.Label("exit"),
		asm.Mov.Reg(asm.R3, asm.R9).WithSymbol("rsi"), asm.Add.Imm(asm.R3, 104), asm.Ja.Label("read"),
		asm.Mov.Reg(asm.R3, asm.R9).WithSymbol("rdi"), asm.Add.Imm(asm.R3, 112),
		asm.Mov.Reg(asm.R1, asm.RFP).WithSymbol("read"), asm.Add.Imm(asm.R1, -32), asm.Mov.Imm(asm.R2, 8),
		asm.FnProbeReadKernel.Call(),
		asm.JNE.Imm(asm.R0, 0, "exit"),
		asm.LoadMem(asm.R9, asm.RFP, -32, asm.DWord), // user pathname pointer
		asm.JEq.Imm(asm.R9, 0, "exit"),
		asm.StoreImm(asm.RFP, -40, 0, asm.Word),
		asm.LoadMapPtr(asm.R1, 0).WithReference("scratch"),
		asm.Mov.Reg(asm.R2, asm.RFP), asm.Add.Imm(asm.R2, -40),
		asm.FnMapLookupElem.Call(),
		asm.JEq.Imm(asm.R0, 0, "exit"),
		asm.Mov.Reg(asm.R8, asm.R0),
		asm.StoreMem(asm.R8, 16, asm.R7, asm.DWord),
		asm.Mov.Reg(asm.R1, asm.R8), asm.Add.Imm(asm.R1, 24), asm.Mov.Imm(asm.R2, scopePathMax), asm.Mov.Reg(asm.R3, asm.R9),
		asm.FnProbeReadUserStr.Call(),
		asm.JSLE.Imm(asm.R0, 1, "exit"),            // failed or empty
		asm.JSGE.Imm(asm.R0, scopePathMax, "exit"), // truncated
		asm.LoadMem(asm.R1, asm.R8, 24, asm.Byte),
		asm.JNE.Imm(asm.R1, '/', "exit"), // only absolute paths
		// epoch: per-CPU counter with the CPU in the top bits, unique per syscall instance.
		asm.LoadMem(asm.R1, asm.R8, 0, asm.DWord), asm.Add.Imm(asm.R1, 1), asm.StoreMem(asm.R8, 0, asm.R1, asm.DWord),
		asm.FnGetSmpProcessorId.Call(),
		asm.LSh.Imm(asm.R0, 48),
		asm.LoadMem(asm.R1, asm.R8, 0, asm.DWord), asm.Or.Reg(asm.R1, asm.R0), asm.StoreMem(asm.R8, 8, asm.R1, asm.DWord),
		asm.LoadMapPtr(asm.R1, 0).WithReference("scopes"),
		asm.Mov.Reg(asm.R2, asm.RFP), asm.Add.Imm(asm.R2, -16), asm.Mov.Reg(asm.R3, asm.R8), asm.Mov.Imm(asm.R4, 0),
		asm.FnMapUpdateElem.Call(),
	}...)
	enter = append(enter, exit...)
	clear := append(prefix(), exit...)
	return &ebpf.CollectionSpec{
		Maps: map[string]*ebpf.MapSpec{
			"views":   {Name: "oac_views", Type: ebpf.Hash, KeySize: 8, ValueSize: 16, MaxEntries: maxViews},
			"scopes":  {Name: "oac_scopes", Type: ebpf.LRUHash, KeySize: scopeKeySize, ValueSize: scopeValueSize, MaxEntries: maxScopes},
			"scratch": {Name: "oac_scratch", Type: ebpf.PerCPUArray, KeySize: 4, ValueSize: scopeValueSize, MaxEntries: 1},
		},
		Programs: map[string]*ebpf.ProgramSpec{
			"scope_enter": {Name: "oac_scope_enter", Type: ebpf.RawTracepoint, AttachTo: "sys_enter", License: "GPL", Instructions: enter},
			"scope_clear": {Name: "oac_scope_clear", Type: ebpf.RawTracepoint, AttachTo: "sys_exit", License: "GPL", Instructions: clear},
		},
	}
}
