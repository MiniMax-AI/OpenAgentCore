// Package viewloader gives an agent-host view the host's ELF loader for its
// dynamic closure binaries, so nothing they load comes from the sandbox.
package viewloader

import "github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"

// MountName is the closure mount that presents the host library directory.
const MountName = "lib"

// Fragment is what a view adds for its dynamic closure binaries. The zero
// Fragment, for static binaries, adds nothing.
type Fragment struct {
	Closure  []agent.ViewMount
	Overlays []agent.ViewOverlay
	Masks    []agent.ViewMask
	// LibraryPath is the view's LD_LIBRARY_PATH, empty for static binaries.
	LibraryPath string
}

// AddTo appends the fragment's mounts, overlays and masks to view.
func (f Fragment) AddTo(view *agent.View) {
	view.Closure = append(view.Closure, f.Closure...)
	view.Overlays = append(view.Overlays, f.Overlays...)
	view.Masks = append(view.Masks, f.Masks...)
}

// fragment presents libDir, which holds every library, and source at the
// interpreter path interp. The masks keep the sandbox's preload list and
// library cache away from the loader.
func fragment(interp, source, libDir string) Fragment {
	lib := agent.ViewMount{Name: MountName, HostDir: libDir}
	return Fragment{
		Closure:     []agent.ViewMount{lib},
		Overlays:    []agent.ViewOverlay{{Path: interp, Source: source, Exec: true}},
		Masks:       []agent.ViewMask{{Path: "/etc/ld.so.preload"}, {Path: "/etc/ld.so.cache"}},
		LibraryPath: lib.Path(),
	}
}
