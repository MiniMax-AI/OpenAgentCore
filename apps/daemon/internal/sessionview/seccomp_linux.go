//go:build linux

package sessionview

import (
	"runtime"
	"unsafe"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// Offsets into struct seccomp_data. On amd64 and arm64, both little-endian, an argument's low word comes first.
const (
	dataNr      = 0
	dataArch    = 4
	dataArg0Low = 16

	x32SyscallBit = 0x40000000
)

// seccompArch holds one architecture's numbers for the syscalls the filter decides on. They come from the kernel's syscall tables, since the filter covers both architectures whatever the build.
type seccompArch struct {
	audit                         uint32
	clone, unshare, setns, clone3 uint32
	x32                           bool
}

var seccompArches = []seccompArch{
	{audit: unix.AUDIT_ARCH_X86_64, clone: 56, unshare: 272, setns: 308, clone3: 435, x32: true},
	{audit: unix.AUDIT_ARCH_AARCH64, clone: 220, unshare: 97, setns: 268, clone3: 435},
}

var (
	retAllow  = uint32(unix.SECCOMP_RET_ALLOW)
	retKill   = uint32(unix.SECCOMP_RET_KILL_PROCESS)
	retEPERM  = uint32(unix.SECCOMP_RET_ERRNO | unix.EPERM)
	retENOSYS = uint32(unix.SECCOMP_RET_ERRNO | unix.ENOSYS)
)

// seccompProgram denies creating a user namespace and every setns and allows everything else. Classic BPF cannot read clone3's argument struct, so clone3 reports ENOSYS and libc and Go fall back to clone. Any architecture other than amd64 and arm64 kills the process, and amd64 x32 syscalls report ENOSYS.
func seccompProgram() []bpf.Instruction {
	prog := []bpf.Instruction{bpf.LoadAbsolute{Off: dataArch, Size: 4}}
	for _, a := range seccompArches {
		// Every path through a block returns, so a mismatch skips to the next architecture with A still holding arch.
		block := a.block()
		prog = append(prog, bpf.JumpIf{Cond: bpf.JumpEqual, Val: a.audit, SkipFalse: uint8(len(block))})
		prog = append(prog, block...)
	}
	return append(prog, bpf.RetConstant{Val: retKill})
}

func (a seccompArch) block() []bpf.Instruction {
	b := []bpf.Instruction{bpf.LoadAbsolute{Off: dataNr, Size: 4}}
	if a.x32 {
		b = append(b, bpf.JumpIf{Cond: bpf.JumpGreaterOrEqual, Val: x32SyscallBit, SkipFalse: 1}, bpf.RetConstant{Val: retENOSYS})
	}
	return append(b,
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: a.clone3, SkipFalse: 1},
		bpf.RetConstant{Val: retENOSYS},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: a.setns, SkipFalse: 1},
		bpf.RetConstant{Val: retEPERM},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: a.clone, SkipTrue: 1},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: a.unshare, SkipFalse: 3},
		bpf.LoadAbsolute{Off: dataArg0Low, Size: 4},
		bpf.JumpIf{Cond: bpf.JumpBitsSet, Val: unix.CLONE_NEWUSER, SkipFalse: 1},
		bpf.RetConstant{Val: retEPERM},
		bpf.RetConstant{Val: retAllow},
	)
}

// installSeccomp sets no_new_privs and installs the filter on the calling thread, reporting failures as kind. Children forked from the thread inherit both, and the filter survives exec.
func installSeccomp(kind error) error {
	raw, err := bpf.Assemble(seccompProgram())
	if err != nil {
		return &Error{Kind: kind, Op: "assemble seccomp filter", Err: err}
	}
	filter := make([]unix.SockFilter, len(raw))
	for i, r := range raw {
		filter[i] = unix.SockFilter{Code: r.Op, Jt: r.Jt, Jf: r.Jf, K: r.K}
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return &Error{Kind: kind, Op: "no_new_privs", Err: err}
	}
	_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, 0, uintptr(unsafe.Pointer(&prog)))
	runtime.KeepAlive(filter)
	if errno != 0 {
		return &Error{Kind: kind, Op: "seccomp", Err: errno}
	}
	return nil
}
