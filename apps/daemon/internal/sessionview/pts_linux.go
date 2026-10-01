//go:build linux

package sessionview

import (
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// ptsOptions configure the view's devpts instance.
var ptsOptions = [][2]string{{"ptmxmode", "0666"}, {"mode", "0620"}}

const ptsAttr = attrNoSuid | attrNoExec

// PTS is the devpts instance a view mounts at /dev/pts. It exists before the view, so its device number can configure what serves the view, such as the process broker, before anything in the view runs.
type PTS struct {
	dev uint64

	mu  sync.Mutex
	mnt *os.File // a detached mount; nil once closed
}

// NewPTS creates a devpts instance for one view.
func NewPTS() (*PTS, error) {
	fd, err := newFS("devpts", ptsOptions, ptsAttr)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, &Error{Kind: ErrLauncher, Op: "stat", Path: "devpts", Err: err}
	}
	return &PTS{dev: st.Dev, mnt: os.NewFile(uintptr(fd), "devpts")}, nil
}

// Device is the instance's device number: the st_dev of its terminals.
func (p *PTS) Device() uint64 { return p.dev }

// Close releases the instance. A view that mounted it keeps it.
func (p *PTS) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mnt == nil {
		return nil
	}
	err := p.mnt.Close()
	p.mnt = nil
	return err
}

func (p *PTS) file() (*os.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mnt == nil {
		return nil, invalid("the devpts instance is closed")
	}
	return p.mnt, nil
}
