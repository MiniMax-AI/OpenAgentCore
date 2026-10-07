//go:build linux

package sessionview

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Launcher file descriptors. The spec travels over a pipe so that nothing about the view appears in argv or the environment. relayFD, the relay's end of its broker connection, is open only when the spec declares a shim.
const (
	specFD    = 3
	controlFD = 4
	stdinFD   = 5
	stdoutFD  = 6
	stderrFD  = 7
	relayFD   = 8
)

// launchSpec is what the daemon sends the launcher: the Spec without its callbacks and files.
type launchSpec struct {
	Staging  string
	Private  []PrivateDir
	Overlays []Overlay
	Shim     Shim
	Command  command
	UID      uint32
	GID      uint32
	Grace    time.Duration
	// EmptyRoot holds the mountpoints of an empty root, which the launcher creates; it is nil for a world.
	EmptyRoot []Mountpoint
}

// command is what a process of the view runs.
type command struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

type msgKind uint8

const (
	msgMounted  msgKind = iota + 1 // launcher: the root is mounted; carries the world's /dev/fuse fd, if any, then the netns fd
	msgProceed                     // daemon: the world serves and the network is set up; carries the mount targets
	msgStarted                     // launcher: the process runs
	msgFailed                      // launcher: construction failed
	msgExited                      // launcher: the process, or the spawned process ID, ended
	msgSignal                      // daemon: signal the process, or the spawned process Spawn
	msgSignaled                    // launcher: whether msgSignal reached it
	msgSpawn                       // daemon: start another process; carries a pipe with its command, then its stdin, stdout and stderr
	msgSpawned                     // launcher: the spawned process's Pid, or why none started
	msgNoRelay                     // launcher: the relay ended before the launcher
)

type message struct {
	Kind      msgKind
	ID        uint64 // pairs a reply with its request; a spawn's ID also names the process it started, and 0 names the process
	Spawn     uint64 // the ID of the spawned process msgSignal signals, or 0
	Pid       int
	Signal    syscall.Signal
	Delivered bool
	Exit      Exit
	Fail      failure
	Targets   map[string]string // each mountpoint's view path to the path the world presents it at
}

// failure carries a launcher *Error across the control socket. A failure to start a command leaves the command out, so that no message grows with what a caller passed: the daemon has the command, and startErr puts it back.
type failure struct {
	Kind  int
	Op    string
	Path  string
	Errno syscall.Errno
	Text  string
}

func failureOf(err error) failure {
	f := failure{Text: err.Error()}
	var e *Error
	if errors.As(err, &e) {
		for i, k := range errorKinds {
			if k == e.Kind {
				f.Kind = i
			}
		}
		f.Op, f.Path, f.Text = e.Op, e.Path, ""
		if e.Err != nil {
			f.Text = e.Err.Error()
		}
	}
	if errors.As(err, &f.Errno) {
		f.Text = ""
	}
	return f
}

func (f failure) err() error {
	e := &Error{Kind: ErrLauncher, Op: f.Op, Path: f.Path}
	if f.Kind > 0 && f.Kind < len(errorKinds) {
		e.Kind = errorKinds[f.Kind]
	}
	switch {
	case f.Errno != 0:
		e.Err = f.Errno
	case f.Text != "":
		e.Err = errors.New(f.Text)
	}
	return e
}

// startErr returns the error f reports while the launcher starts c, with the directory or path of c that a failed chdir or exec concerns.
func (f failure) startErr(c command) error {
	if f.Path == "" {
		switch f.Op {
		case "chdir":
			f.Path = c.Dir
		case "exec":
			f.Path = c.Path
		}
	}
	return f.err()
}

// control is one end of the launcher's SOCK_SEQPACKET control socket. Each packet holds one gob-encoded message.
type control struct {
	conn    *net.UnixConn
	sending chan struct{} // held by the send in progress
}

func newControl(f *os.File) (*control, error) {
	c, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		c.Close()
		return nil, fmt.Errorf("control socket is a %T", c)
	}
	return &control{conn: uc, sending: make(chan struct{}, 1)}, nil
}

// send sends m with fds. It returns ctx's error when ctx ends before m is sent, whether it waits for another send or for room on the socket; a packet goes whole or not at all. Once the socket is shut down, a waiting send fails.
func (c *control) send(ctx context.Context, m message, fds ...int) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(m); err != nil {
		return err
	}
	var oob []byte
	if len(fds) > 0 {
		oob = unix.UnixRights(fds...)
	}
	select {
	case c.sending <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.sending }()
	expired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		c.conn.SetWriteDeadline(time.Unix(1, 0))
		close(expired)
	})
	_, _, err := c.conn.WriteMsgUnix(buf.Bytes(), oob, nil)
	if !stop() {
		<-expired
		c.conn.SetWriteDeadline(time.Time{})
		if err != nil {
			err = ctx.Err()
		}
	}
	return err
}

// recv returns the next message and the files it carries. It returns io.EOF once the peer has closed its end.
func (c *control) recv() (message, []*os.File, error) {
	buf := make([]byte, 64<<10)
	oob := make([]byte, unix.CmsgSpace(4*4))
	n, oobn, flags, _, err := c.conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return message{}, nil, err
	}
	files, err := unixRights(oob[:oobn])
	if err == nil && flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
		err = errors.New("control message truncated")
	}
	if err == nil && n == 0 {
		err = io.EOF
	}
	var m message
	if err == nil {
		err = gob.NewDecoder(bytes.NewReader(buf[:n])).Decode(&m)
	}
	if err != nil {
		closeFiles(files)
		return message{}, nil, err
	}
	return m, files, nil
}

// interrupt shuts the socket down in both directions, whoever else holds it, so that a pending recv at either end returns io.EOF and every later send fails.
func (c *control) interrupt() {
	c.conn.CloseRead()
	c.conn.CloseWrite()
}

func (c *control) close() error {
	return c.conn.Close()
}

func unixRights(oob []byte) ([]*os.File, error) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var files []*os.File
	for i := range msgs {
		fds, err := unix.ParseUnixRights(&msgs[i])
		if err != nil {
			closeFiles(files)
			return nil, err
		}
		for _, fd := range fds {
			files = append(files, os.NewFile(uintptr(fd), "sessionview-fd"))
		}
	}
	return files, nil
}

func closeFiles(files []*os.File) {
	for _, f := range files {
		if f != nil {
			f.Close()
		}
	}
}
