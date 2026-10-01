//go:build linux

package worldfs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"syscall"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
)

// maxHops is how many symlinks resolving one mountpoint may traverse, the kernel's MAXSYMLINKS.
const maxHops = 40

// present resolves each mountpoint inside the world within ctx and installs the synthetic nodes the view needs. It runs before serving starts, so it needs no locks.
func (f *frontend) present(ctx context.Context, mps []sessionview.Mountpoint) (sessionview.Presentation, error) {
	var p sessionview.Presentation
	for _, mp := range mps {
		r := resolver{ctx: ctx, f: f, p: &p, mp: mp}
		target, err := r.resolve()
		r.dropAhead()
		if err != nil {
			return sessionview.Presentation{}, err
		}
		p.Targets = append(p.Targets, target)
	}
	for _, n := range f.nodes {
		for name := range n.fixed {
			n.order = append(n.order, name)
		}
		slices.Sort(n.order)
	}
	return p, nil
}

type step struct {
	n    *inode
	name string
}

// resolver walks one mountpoint's path from the world root, like the kernel would in the view.
type resolver struct {
	ctx   context.Context
	f     *frontend
	p     *sessionview.Presentation
	mp    sessionview.Mountpoint
	stack []step
	queue []string
	hops  int
	// ahead holds entries one Walk returned for the next names in queue, and the failure that stopped it.
	ahead    []sandboxfs.Entry
	aheadErr error
}

func (r *resolver) resolve() (string, error) {
	comps := strings.Split(strings.TrimPrefix(r.mp.Path, "/"), "/")
	final := comps[len(comps)-1]
	if !isName(final) {
		return "", r.fail(syscall.EINVAL)
	}
	r.stack = []step{{n: r.f.root}}
	r.queue = slices.Clone(comps[:len(comps)-1])
	for len(r.queue) > 0 {
		name := r.queue[0]
		r.queue = r.queue[1:]
		switch name {
		case "", ".":
			continue
		case "..":
			if len(r.stack) > 1 {
				r.stack = r.stack[:len(r.stack)-1]
			}
			continue
		}
		child, err := r.child(name)
		if err != nil {
			return "", err
		}
		switch {
		case child.mount:
			return "", r.fail(fmt.Errorf("%s is the mountpoint %s", r.at(name), child.path))
		case child.link != nil:
			if r.hops++; r.hops > maxHops {
				return "", r.fail(syscall.ELOOP)
			}
			r.dropAhead()
			if child.link[0] == '/' {
				r.stack = r.stack[:1]
			}
			r.queue = append(strings.Split(string(child.link), "/"), r.queue...)
		case child.fileType() != sandboxfs.ModeDirectory:
			return "", r.fail(syscall.ENOTDIR)
		default:
			r.stack = append(r.stack, step{child, name})
		}
	}
	cur, at := r.top(), r.at(final)
	if c := cur.fixed[final]; c != nil {
		return "", r.fail(fmt.Errorf("%s is already presented for %s", at, c.path))
	}
	typ := sandboxfs.ModeRegular
	if r.mp.Dir {
		typ = sandboxfs.ModeDirectory
	}
	r.synthetic(cur, final, typ).mount = true
	return at, nil
}

// child returns cur's entry name: a presented node, a pinned sandbox entry, or a synthetic directory when the sandbox confirms the name is missing.
func (r *resolver) child(name string) (*inode, error) {
	cur := r.top()
	if c := cur.fixed[name]; c != nil {
		r.dropAhead()
		return c, nil
	}
	if cur.synthetic() {
		return r.missing(cur, name), nil
	}
	if len(r.ahead) == 0 && r.aheadErr == nil {
		r.walk(name)
	}
	if len(r.ahead) == 0 {
		err := r.aheadErr
		r.aheadErr = nil
		if !isErrno(err, sandboxfs.ErrnoNotFound) {
			return nil, r.serverErr(err)
		}
		return r.missing(cur, name), nil
	}
	e := r.ahead[0]
	r.ahead = r.ahead[1:]
	return r.pin(cur, name, e)
}

// walk looks up name and the plain names after it in one request.
func (r *resolver) walk(name string) {
	names := [][]byte{[]byte(name)}
	for _, n := range r.queue {
		if !isName(n) || len(names) == int(r.f.caps.MaxWalkComponents) {
			break
		}
		names = append(names, []byte(n))
	}
	resp, err := call(r.f, r.ctx, (*sandboxfs.Client).Walk, &sandboxfs.WalkRequest{Parent: r.top().ref, Names: names})
	switch {
	case err != nil:
		r.aheadErr = err
	case resp.Failure != nil:
		r.ahead, r.aheadErr = resp.Entries, resp.Failure
	default:
		r.ahead = resp.Entries
	}
}

// dropAhead releases the entries walked for names the resolution no longer reaches.
func (r *resolver) dropAhead() {
	for _, e := range r.ahead {
		r.f.unref(e.Node, 1)
	}
	r.ahead, r.aheadErr = nil, nil
}

// pin keeps a sandbox entry on the way to a mountpoint for the view's lifetime. A symlink keeps its target too.
func (r *resolver) pin(cur *inode, name string, e sandboxfs.Entry) (*inode, error) {
	n := r.f.byRef[e.Node]
	if n != nil {
		r.f.unref(e.Node, 1)
	} else {
		n = r.f.newInode(e.Node)
		n.attr, n.path = e.Attr, r.at(name)
	}
	n.held = true
	r.fixed(cur, name, n)
	if n.fileType() != sandboxfs.ModeSymlink || n.link != nil {
		return n, nil
	}
	resp, err := call(r.f, r.ctx, (*sandboxfs.Client).Readlink, &sandboxfs.ReadlinkRequest{Node: n.ref})
	if err != nil {
		return nil, r.serverErr(err)
	}
	if len(resp.Target) == 0 {
		return nil, r.fail(syscall.ENOENT)
	}
	n.link = resp.Target
	r.p.Links = append(r.p.Links, sessionview.PresentedLink{Path: n.path, Target: string(n.link)})
	return n, nil
}

func (r *resolver) missing(cur *inode, name string) *inode {
	n := r.synthetic(cur, name, sandboxfs.ModeDirectory)
	r.p.Synthesized = append(r.p.Synthesized, n.path)
	return n
}

func (r *resolver) synthetic(cur *inode, name string, typ uint32) *inode {
	n := r.f.newInode(sandboxfs.NodeRef{})
	n.synth, n.held, n.path = typ, true, r.at(name)
	r.fixed(cur, name, n)
	return n
}

func (r *resolver) fixed(cur *inode, name string, n *inode) {
	if cur.fixed == nil {
		cur.fixed = map[string]*inode{}
	}
	cur.fixed[name] = n
}

func (r *resolver) top() *inode { return r.stack[len(r.stack)-1].n }

// at is the in-root path of name in the current directory.
func (r *resolver) at(name string) string {
	var b strings.Builder
	for _, s := range r.stack[1:] {
		b.WriteString("/" + s.name)
	}
	return b.String() + "/" + name
}

func (r *resolver) fail(err error) error {
	return &Error{Kind: ErrMountpoint, Op: "present", Path: r.mp.Path, Err: err}
}

// serverErr reports a sandbox errno as a presentation failure and anything else as a connection failure.
func (r *resolver) serverErr(err error) error {
	var fail *sandboxfs.Failure
	if errors.As(err, &fail) && fail.Code == sandboxfs.CodeErrno {
		return r.fail(fmt.Errorf("%w (%w)", errnoOf(err), err))
	}
	return &Error{Kind: ErrConnect, Op: "present", Path: r.mp.Path, Err: err}
}

func isName(s string) bool { return s != "" && s != "." && s != ".." }

func isErrno(err error, errno sandboxfs.Errno) bool {
	var fail *sandboxfs.Failure
	return errors.As(err, &fail) && fail.Code == sandboxfs.CodeErrno && fail.Errno == errno
}
