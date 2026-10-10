//go:build linux

package nfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const rootMarker = ".oac-storage-root"

func unavailable(err error) error { return fmt.Errorf("%w: %w", workspacefs.ErrUnavailable, err) }
func unconfirmed(err error) error { return fmt.Errorf("%w: %w", workspacefs.ErrUnconfirmed, err) }

// open validates the pinned directory descriptor before any namespace mutation.
// It runs inside the process budget, including OpenRoot and every stat/read.
func (a *Adapter) open() (*os.Root, error) {
	r, err := os.OpenRoot(a.parameters.Root)
	if err != nil {
		return nil, unavailable(err)
	}
	fail := func(err error) (*os.Root, error) { r.Close(); return nil, unavailable(err) }
	f, err := r.Open(".")
	if err != nil {
		return fail(err)
	}
	var stat unix.Statfs_t
	err = unix.Fstatfs(int(f.Fd()), &stat)
	f.Close()
	if err != nil {
		return fail(err)
	}
	if stat.Type != unix.NFS_SUPER_MAGIC {
		return fail(errors.New("root is not kernel NFS"))
	}
	if os.Geteuid() != int(a.parameters.UID) {
		return fail(errors.New("effective UID differs from configured nonroot principal"))
	}
	if err = private(r, ".", true, a.parameters.UID); err != nil {
		return fail(err)
	}
	if err = private(r, rootMarker, false, a.parameters.UID); err != nil {
		return fail(err)
	}
	raw, err := r.ReadFile(rootMarker)
	if err != nil {
		return fail(err)
	}
	if !bytes.Equal(raw, []byte(a.parameters.NamespaceID+"\n")) {
		return fail(errors.New("namespace marker mismatch"))
	}
	if err = mkdir(r, "objects", a.parameters.UID); err != nil {
		return fail(err)
	}
	if err = syncDir(r, "."); err != nil {
		return fail(err)
	}
	return r, nil
}
func private(r *os.Root, name string, dir bool, uid uint32) error {
	info, err := r.Lstat(name)
	if err != nil {
		return err
	}
	// FileInfo uses syscall.Stat_t; descriptor fstat avoids platform representation assertions.
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != dir || (!dir && !info.Mode().IsRegular()) {
		return workspacefs.ErrOwnership
	}
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	var st unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &st); err != nil {
		return err
	}
	expected := uint32(0600)
	if dir {
		expected = 0700
	}
	if st.Uid != uid || st.Mode&0777 != expected {
		return fmt.Errorf("%w: private owner or mode on %s", workspacefs.ErrOwnership, name)
	}
	return nil
}
func mkdir(r *os.Root, name string, uid uint32) error {
	if err := r.Mkdir(name, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return private(r, name, true, uid)
}
func syncDir(r *os.Root, name string) error {
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func write(r *os.Root, name string, raw []byte) error {
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (a *Adapter) Check(ctx context.Context) error {
	_, err := run(ctx, func() (struct{}, error) {
		r, err := a.open()
		if err != nil {
			return struct{}{}, err
		}
		defer r.Close()
		name := ".probe-" + uuid.NewString()
		f, err := r.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return struct{}{}, unavailable(err)
		}
		defer r.Remove(name)
		defer f.Close()
		fd := int(f.Fd())
		key := "user.oac_workspace_check"
		value := []byte("workspace")
		if err = unix.Fsetxattr(fd, key, value, 0); err != nil {
			return struct{}{}, unavailable(err)
		}
		buf := make([]byte, 64)
		n, err := unix.Fgetxattr(fd, key, buf)
		if err != nil {
			return struct{}{}, unavailable(err)
		}
		if !bytes.Equal(buf[:n], value) {
			return struct{}{}, unavailable(errors.New("xattr mismatch"))
		}
		if err = unix.Fremovexattr(fd, key); err != nil {
			return struct{}{}, unavailable(err)
		}
		return struct{}{}, nil
	})
	return err
}

func object(ref workspacefs.Reference) string { return path.Join("objects", ref.ObjectID) }
func identity(r *os.Root, name string, ref workspacefs.Reference) error {
	raw, err := r.ReadFile(name)
	if err != nil {
		return err
	}
	var got workspacefs.Reference
	if strict(raw, &got) != nil || got != ref {
		return workspacefs.ErrOwnership
	}
	return nil
}

// claim publishes an immutable synced identity with a no-replace hard link.
func (a *Adapter) claim(r *os.Root, ref workspacefs.Reference) error {
	obj := object(ref)
	if err := mkdir(r, obj, a.parameters.UID); err != nil {
		return err
	}
	name := path.Join(obj, "identity")
	if err := identity(r, name, ref); err == nil {
		if err := syncDir(r, obj); err != nil {
			return err
		}
		return syncDir(r, "objects")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tmp := path.Join(obj, ".identity-"+uuid.NewString())
	raw, _ := json.Marshal(ref)
	if err := write(r, tmp, raw); err != nil {
		return err
	}
	defer r.Remove(tmp)
	if err := r.Link(tmp, name); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := identity(r, name, ref); err != nil {
		return err
	}
	if err := syncDir(r, obj); err != nil {
		return err
	}
	return syncDir(r, "objects")
}
func retired(r *os.Root, obj string) (bool, error) {
	info, err := r.Lstat(path.Join(obj, "deleted-marker"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, workspacefs.ErrOwnership
	}
	return true, nil
}
func live(r *os.Root, ref workspacefs.Reference) error {
	obj := object(ref)
	if err := identity(r, path.Join(obj, "identity"), ref); err != nil {
		return err
	}
	dead, err := retired(r, obj)
	if err != nil {
		return err
	}
	if dead {
		return workspacefs.ErrNotFound
	}
	dir := path.Join(obj, "live")
	info, err := r.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return workspacefs.ErrOwnership
	}
	if err := identity(r, path.Join(dir, "identity"), ref); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return workspacefs.ErrOwnership
		}
		return err
	}
	return nil
}
func (a *Adapter) Observe(ctx context.Context, ref workspacefs.Reference) (workspacefs.Attachment, error) {
	if err := ref.Validate(); err != nil {
		return workspacefs.Attachment{}, err
	}
	return run(ctx, func() (workspacefs.Attachment, error) {
		r, err := a.open()
		if err != nil {
			return workspacefs.Attachment{}, err
		}
		defer r.Close()
		if err = live(r, ref); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				err = workspacefs.ErrNotFound
			}
			if !errors.Is(err, workspacefs.ErrNotFound) && !errors.Is(err, workspacefs.ErrOwnership) {
				err = unavailable(err)
			}
			return workspacefs.Attachment{}, err
		}
		if err = private(r, path.Join(object(ref), "live", "data"), true, a.parameters.UID); err != nil {
			return workspacefs.Attachment{}, unavailable(err)
		}
		return a.attachment(ref), nil
	})
}
func (a *Adapter) Create(ctx context.Context, ref workspacefs.Reference) (workspacefs.Attachment, error) {
	if err := ref.Validate(); err != nil {
		return workspacefs.Attachment{}, err
	}
	return run(ctx, func() (workspacefs.Attachment, error) {
		r, err := a.open()
		if err != nil {
			return workspacefs.Attachment{}, err
		}
		defer r.Close()
		if err = a.create(r, ref); err != nil {
			return workspacefs.Attachment{}, err
		}
		return a.attachment(ref), nil
	})
}
func (a *Adapter) create(r *os.Root, ref workspacefs.Reference) error {
	if err := a.claim(r, ref); err != nil {
		return unconfirmed(err)
	}
	obj := object(ref)
	dead, err := retired(r, obj)
	if err != nil {
		return unconfirmed(err)
	}
	if dead {
		return workspacefs.ErrNotFound
	}
	if err = live(r, ref); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, dir := range []string{"staging", "trash"} {
		if err = mkdir(r, path.Join(obj, dir), a.parameters.UID); err != nil {
			return unconfirmed(err)
		}
	}
	stage := path.Join(obj, "staging", uuid.NewString())
	if err = r.Mkdir(stage, 0700); err != nil {
		return unconfirmed(err)
	}
	if err = r.Mkdir(path.Join(stage, "data"), 0700); err != nil {
		return unconfirmed(err)
	}
	raw, _ := json.Marshal(ref)
	if err = write(r, path.Join(stage, "identity"), raw); err != nil {
		return unconfirmed(err)
	}
	if err = syncDir(r, path.Join(stage, "data")); err != nil {
		return unconfirmed(err)
	}
	if err = syncDir(r, stage); err != nil {
		return unconfirmed(err)
	}
	dead, err = retired(r, obj)
	if err != nil {
		return unconfirmed(err)
	}
	if dead {
		_ = removeEnvelope(r, stage, ref, false)
		return workspacefs.ErrNotFound
	}
	if err = r.Rename(stage, path.Join(obj, "live")); err != nil {
		if check := live(r, ref); check != nil {
			return unconfirmed(err)
		}
		if err = removeEnvelope(r, stage, ref, false); err != nil {
			return unconfirmed(err)
		}
	} else if err = syncDir(r, obj); err != nil {
		return unconfirmed(err)
	}
	dead, err = retired(r, obj)
	if err != nil {
		return unconfirmed(err)
	}
	if dead {
		// The terminal marker prevents admission. Only remove an empty result;
		// any unexpected user data is retained for a subsequent Delete.
		if err := removeEnvelope(r, path.Join(obj, "live"), ref, false); err != nil {
			return unconfirmed(err)
		}
		return workspacefs.ErrNotFound
	}
	return nil
}

// removeEnvelope removes data before identity. Missing markers permit removal
// of empty shells only, including when another deleter has already finished.
func removeEnvelope(r *os.Root, dir string, ref workspacefs.Reference, recursive bool) error {
	err := identity(r, path.Join(dir, "identity"), ref)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	data := path.Join(dir, "data")
	if err == nil && recursive {
		if err = r.RemoveAll(data); err != nil {
			return err
		}
	} else {
		if err = r.Remove(data); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err = r.Remove(path.Join(dir, "identity")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err = r.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
func (a *Adapter) Delete(ctx context.Context, ref workspacefs.Reference) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	_, err := run(ctx, func() (struct{}, error) {
		r, err := a.open()
		if err != nil {
			return struct{}{}, err
		}
		defer r.Close()
		err = a.delete(r, ref)
		return struct{}{}, err
	})
	return err
}
func (a *Adapter) delete(r *os.Root, ref workspacefs.Reference) error {
	if err := a.claim(r, ref); err != nil {
		return unconfirmed(err)
	}
	obj := object(ref)
	marker := path.Join(obj, "deleted-marker")
	if err := write(r, marker, []byte("deleted\n")); err != nil && !errors.Is(err, fs.ErrExist) {
		return unconfirmed(err)
	}
	if err := syncDir(r, marker); err != nil {
		return unconfirmed(err)
	}
	if err := syncDir(r, obj); err != nil {
		return unconfirmed(err)
	}
	for _, dir := range []string{"staging", "trash"} {
		if err := mkdir(r, path.Join(obj, dir), a.parameters.UID); err != nil {
			return unconfirmed(err)
		}
	}
	liveDir := path.Join(obj, "live")
	if err := identity(r, path.Join(liveDir, "identity"), ref); err == nil {
		if err = r.Rename(liveDir, path.Join(obj, "trash", uuid.NewString())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return unconfirmed(err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := syncDir(r, obj); err != nil {
		return unconfirmed(err)
	}
	if err := syncDir(r, path.Join(obj, "trash")); err != nil {
		return unconfirmed(err)
	}
	for _, parent := range []string{"trash", "staging"} {
		dir, err := r.Open(path.Join(obj, parent))
		if err != nil {
			return unconfirmed(err)
		}
		entries, err := dir.ReadDir(-1)
		dir.Close()
		if err != nil {
			return unconfirmed(err)
		}
		for _, entry := range entries {
			if err = removeEnvelope(r, path.Join(obj, parent, entry.Name()), ref, parent == "trash"); err != nil {
				return unconfirmed(err)
			}
		}
		if err = syncDir(r, path.Join(obj, parent)); err != nil {
			return unconfirmed(err)
		}
	}
	// A late in-flight Create cannot be admitted after the durable marker. Its
	// empty envelope can require a repeated Delete after that syscall settles.
	if _, err := r.Lstat(liveDir); err == nil {
		return unconfirmed(errors.New("late live publication"))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return unconfirmed(err)
	}
	return nil
}
