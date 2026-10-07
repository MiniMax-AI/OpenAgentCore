package localworkspace

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// ValidPath reports whether p is a workspace path as the API addresses it:
// slash-separated plain names below the workspace, without a backslash, NUL,
// CR or LF.
func ValidPath(p string) bool {
	return p != "." && len(p) <= 4096 && fs.ValidPath(p) && !strings.ContainsAny(p, "\\\x00\r\n")
}

// Logical API paths stay slash-separated on every host. os.Root anchors API
// file operations to the selected workspace; it does not constrain native tools.
func nativeAPIPath(path string) (string, error) {
	if path == "" {
		return ".", nil
	}
	if !ValidPath(path) {
		return "", fs.ErrInvalid
	}
	return filepath.Localize(path)
}

func (b *Binding) listNativeDirectory(ctx context.Context, path string, limit int) (dispatch.WorkspaceDirectoryResult, error) {
	result := dispatch.WorkspaceDirectoryResult{Entries: []dispatch.WorkspaceDirectoryEntry{}}
	if b == nil || ctx.Err() != nil {
		return result, dispatch.ErrWorkspaceReadUnavailable
	}
	local, err := nativeAPIPath(path)
	if err != nil {
		return result, dispatch.ErrWorkspaceReadInvalid
	}
	root, err := os.OpenRoot(b.workspace)
	if err != nil {
		return result, dispatch.ErrWorkspaceReadUnavailable
	}
	defer root.Close()
	if local != "." {
		partial := ""
		for _, part := range strings.Split(path, "/") {
			partial = filepath.Join(partial, part)
			info, err := root.Lstat(partial)
			if errors.Is(err, fs.ErrPermission) {
				return result, fs.ErrPermission
			}
			if err != nil || !info.IsDir() {
				return result, dispatch.ErrWorkspaceNotDirectory
			}
		}
	}
	dir, err := openNativePath(root, local)
	if errors.Is(err, fs.ErrPermission) {
		return result, fs.ErrPermission
	}
	if err != nil {
		return result, dispatch.ErrWorkspaceNotDirectory
	}
	defer dir.Close()
	entries, err := dir.ReadDir(limit + 1)
	if err != nil && err != io.EOF {
		return result, dispatch.ErrWorkspaceNotDirectory
	}
	if ctx.Err() != nil {
		return result, dispatch.ErrWorkspaceReadUnavailable
	}
	result.Truncated = len(entries) > limit
	if result.Truncated {
		entries = entries[:limit]
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	wire := &proto.WorkspaceDirectoryResult{Entries: []proto.WorkspaceDirectoryEntry{}, Truncated: result.Truncated}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return result, dispatch.ErrWorkspaceReadUncertain
		}
		item := proto.WorkspaceDirectoryEntry{Name: entry.Name(), Kind: "other"}
		switch {
		case info.Mode().IsRegular():
			item.Kind = "file"
			size := info.Size()
			item.SizeBytes = &size
		case info.IsDir():
			item.Kind = "directory"
		case info.Mode()&os.ModeSymlink != 0:
			item.Kind = "symlink"
		}
		wire.Entries = append(wire.Entries, item)
	}
	if !proto.ValidWorkspaceDirectory(wire, limit) {
		return result, dispatch.ErrWorkspaceReadInvalid
	}
	for _, item := range wire.Entries {
		result.Entries = append(result.Entries, dispatch.WorkspaceDirectoryEntry{Name: item.Name, Kind: item.Kind, SizeBytes: item.SizeBytes})
	}
	return result, nil
}

func (b *Binding) writeNativeFile(ctx context.Context, path string, data []byte) (dispatch.WorkspaceWriteResult, error) {
	result := dispatch.WorkspaceWriteResult{}
	local, err := nativeAPIPath(path)
	if err != nil {
		return result, dispatch.ErrWorkspaceWriteInvalid
	}
	root, err := os.OpenRoot(b.workspace)
	if err != nil {
		return result, dispatch.ErrEnvironmentUnavailable
	}
	defer root.Close()
	if err = root.MkdirAll(filepath.Dir(local), 0700); err != nil {
		return result, dispatch.ErrWorkspaceWriteRejected
	}
	temporary := ".oac-write-" + uuid.NewString()
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, dispatch.ErrWorkspaceWriteRejected
	}
	defer root.Remove(temporary)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return result, dispatch.ErrWorkspaceWriteRejected
	}
	// Link publishes complete bytes without replacing an existing destination.
	if err = root.Link(temporary, local); err != nil {
		if info, e := root.Lstat(local); e == nil {
			if info.IsDir() {
				return result, dispatch.ErrWorkspaceWriteDirectory
			}
			return result, dispatch.ErrWorkspaceWriteUnsafe
		}
		return result, dispatch.ErrWorkspaceWriteRejected
	}
	return dispatch.WorkspaceWriteResult{SizeBytes: int64(len(data))}, nil
}

// The bounds of an output export: each file's bytes, the export's bytes, its
// entries and its directory depth.
const (
	ExportFileBytes  int64 = 200 << 20
	ExportBatchBytes int64 = 500 << 20
	ExportEntries          = 4096
	ExportDepth            = 64
)

type nativeExport struct {
	ctx     context.Context
	root    *os.Root
	archive *tar.Writer
	entries int
	bytes   int64
}

func (b *Binding) ExportOutputs(ctx context.Context, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(b.workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	archive := tar.NewWriter(output)
	info, err := root.Lstat("outputs")
	if errors.Is(err, fs.ErrNotExist) {
		return archive.Close()
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("workspace outputs is not a directory")
	}
	exporter := nativeExport{ctx: ctx, root: root, archive: archive}
	if err = exporter.walk("outputs", 0); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return archive.Close()
}
func (x *nativeExport) walk(path string, depth int) error {
	if err := x.ctx.Err(); err != nil {
		return err
	}
	if depth > ExportDepth || len(path) > 4096 {
		return errors.New("workspace export exceeds traversal bound")
	}
	dir, err := openNativePath(x.root, path)
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(ExportEntries + 1)
	_ = dir.Close()
	if err != nil && err != io.EOF {
		return err
	}
	x.entries += len(entries)
	if x.entries > ExportEntries {
		return errors.New("workspace export exceeds entry bound")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := path + "/" + entry.Name()
		if _, err := nativeAPIPath(name); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			continue
		case info.IsDir():
			if err = x.walk(name, depth+1); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if err = x.append(name); err != nil {
				return err
			}
		default:
			return errors.New("workspace output is not a regular file")
		}
	}
	return nil
}
func (x *nativeExport) append(path string) error {
	if err := x.ctx.Err(); err != nil {
		return err
	}
	file, err := openNativePath(x.root, path)
	if err != nil {
		return err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return err
	}
	size := before.Size()
	if !before.Mode().IsRegular() || size < 0 || size > ExportFileBytes || size > ExportBatchBytes-x.bytes {
		return errors.New("workspace export exceeds file bound")
	}
	x.bytes += size
	if err = x.archive.WriteHeader(&tar.Header{Name: path, Typeflag: tar.TypeReg, Mode: 0600, Size: size, Format: tar.FormatPAX}); err != nil {
		return err
	}
	if _, err = io.CopyN(x.archive, nativeContextReader{ctx: x.ctx, reader: file}, size); err != nil {
		return err
	}
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if after.Size() != size || !after.ModTime().Equal(before.ModTime()) {
		return errors.New("workspace output changed during export")
	}
	return nil
}

type nativeContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r nativeContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
