//go:build linux

package fileservice

import (
	"bytes"
	"encoding/binary"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"golang.org/x/sys/unix"
)

// cursor is a directory handle's getdents position. A cookie is the kernel's
// d_off of the entry it follows, so resuming at another cookie is one lseek.
type cursor struct {
	dev uint64 // the directory's device, for composing entry inode numbers

	mu   sync.Mutex
	buf  []byte
	rest []byte // entries read from the kernel and not yet returned
	pos  uint64 // cookie of the last returned or skipped entry
}

type rawEntry struct {
	ino  uint64
	off  uint64
	typ  uint8
	name []byte
}

// next parses one linux_dirent64 from c.rest.
func (c *cursor) next() (rawEntry, []byte, error) {
	const header = 19 // d_ino, d_off, d_reclen, d_type
	if len(c.rest) < header {
		return rawEntry{}, nil, unix.EIO
	}
	reclen := int(binary.NativeEndian.Uint16(c.rest[16:18]))
	if reclen < header || reclen > len(c.rest) {
		return rawEntry{}, nil, unix.EIO
	}
	name := c.rest[header:reclen]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	e := rawEntry{ino: binary.NativeEndian.Uint64(c.rest[0:8]), off: binary.NativeEndian.Uint64(c.rest[8:16]), typ: c.rest[18], name: name}
	return e, c.rest[reclen:], nil
}

var direntTypes = map[uint8]uint32{
	unix.DT_REG: sandboxfs.ModeRegular, unix.DT_DIR: sandboxfs.ModeDirectory, unix.DT_LNK: sandboxfs.ModeSymlink,
	unix.DT_FIFO: sandboxfs.ModeFIFO, unix.DT_SOCK: sandboxfs.ModeSocket, unix.DT_CHR: sandboxfs.ModeCharDevice,
	unix.DT_BLK: sandboxfs.ModeBlockDevice,
}

// read returns the entries after r.Cookie that fit r.Limit. It skips "." and
// "..", and entries removed before they could be described. A directory that
// changes while it is read can repeat a name or a cookie, so a page ends
// before a repeat.
func (c *cursor) read(st *state, fd int, r *sandboxfs.ReadDirRequest) (*sandboxfs.ReadDirResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.Cookie != c.pos {
		if _, err := unix.Seek(fd, int64(r.Cookie), unix.SEEK_SET); err != nil {
			return nil, err
		}
		c.rest, c.pos = nil, r.Cookie
	}
	resp := &sandboxfs.ReadDirResponse{}
	size := 0
	names, cookies := map[string]bool{}, map[uint64]bool{}
	for {
		if len(c.rest) == 0 {
			if c.buf == nil {
				c.buf = make([]byte, 32<<10)
			}
			n, err := eintr(func() (int, error) { return unix.Getdents(fd, c.buf) })
			if err != nil {
				if len(resp.Entries) > 0 {
					return resp, nil
				}
				return nil, err
			}
			if n == 0 {
				resp.End = true
				return resp, nil
			}
			c.rest = c.buf[:n]
		}
		raw, rest, err := c.next()
		if err != nil {
			c.rest = nil
			return nil, err
		}
		skip := func() { c.rest, c.pos = rest, raw.off }
		if string(raw.name) == "." || string(raw.name) == ".." || raw.typ == unix.DT_WHT {
			skip()
			continue
		}
		if names[string(raw.name)] || cookies[raw.off] {
			return resp, nil
		}
		e := sandboxfs.DirEntry{Name: bytes.Clone(raw.name), Ino: st.ino(c.dev, raw.ino), Type: direntTypes[raw.typ], Cookie: raw.off}
		if r.WithAttrs {
			e.Entry = &sandboxfs.Entry{}
		}
		if size+e.WireSize() > int(r.Limit) {
			if len(resp.Entries) == 0 {
				return nil, unix.EINVAL
			}
			return resp, nil
		}
		switch {
		case r.WithAttrs:
			_, entry, err := st.lookup(fd, raw.name)
			if err == unix.ENOENT {
				skip()
				continue
			}
			if err != nil {
				if len(resp.Entries) > 0 {
					return resp, nil
				}
				return nil, err
			}
			*e.Entry = entry
			e.Ino, e.Type = entry.Attr.Ino, entry.Attr.Mode&sandboxfs.ModeType
		case e.Type == 0:
			var sb unix.Stat_t
			if err := unix.Fstatat(fd, string(raw.name), &sb, unix.AT_SYMLINK_NOFOLLOW); err == unix.ENOENT {
				skip()
				continue
			} else if err != nil {
				if len(resp.Entries) > 0 {
					return resp, nil
				}
				return nil, err
			}
			e.Type = sb.Mode & sandboxfs.ModeType
		}
		resp.Entries = append(resp.Entries, e)
		names[string(e.Name)], cookies[e.Cookie] = true, true
		size += e.WireSize()
		skip()
	}
}
