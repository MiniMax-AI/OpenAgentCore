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

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// Logical API paths stay slash-separated on every host. os.Root anchors API
// file operations to the selected workspace; it does not constrain native tools.
func nativeAPIPath(path string) (string, error) {
	if path == "" {
		return ".", nil
	}
	if path == "." || len(path) > 4096 || !fs.ValidPath(path) || strings.ContainsAny(path, "\\\x00\r\n") {
		return "", fs.ErrInvalid
	}
	return filepath.Localize(path)
}

func (b *Binding) listNativeDirectory(ctx context.Context, path string, limit int) (agent.WorkspaceDirectoryResult, error) {
	result := agent.WorkspaceDirectoryResult{Entries: []agent.WorkspaceDirectoryEntry{}}
	if b == nil || ctx.Err() != nil {
		return result, agent.ErrWorkspaceReadUnavailable
	}
	local, err := nativeAPIPath(path)
	if err != nil {
		return result, agent.ErrWorkspaceReadInvalid
	}
	root, err := os.OpenRoot(b.workspace)
	if err != nil {
		return result, agent.ErrWorkspaceReadUnavailable
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
				return result, agent.ErrWorkspaceNotDirectory
			}
		}
	}
	dir, err := openNativePath(root, local)
	if errors.Is(err, fs.ErrPermission) {
		return result, fs.ErrPermission
	}
	if err != nil {
		return result, agent.ErrWorkspaceNotDirectory
	}
	defer dir.Close()
	entries, err := dir.ReadDir(limit + 1)
	if err != nil && err != io.EOF {
		return result, agent.ErrWorkspaceNotDirectory
	}
	if ctx.Err() != nil {
		return result, agent.ErrWorkspaceReadUnavailable
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
			return result, agent.ErrWorkspaceReadUncertain
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
		return result, agent.ErrWorkspaceReadInvalid
	}
	for _, item := range wire.Entries {
		result.Entries = append(result.Entries, agent.WorkspaceDirectoryEntry{Name: item.Name, Kind: item.Kind, SizeBytes: item.SizeBytes})
	}
	return result, nil
}

func (b *Binding) writeNativeFile(ctx context.Context, path string, data []byte) (agent.WorkspaceWriteResult, error) {
	result := agent.WorkspaceWriteResult{}
	local, err := nativeAPIPath(path)
	if err != nil {
		return result, agent.ErrWorkspaceWriteInvalid
	}
	root, err := os.OpenRoot(b.workspace)
	if err != nil {
		return result, agent.ErrWorkspaceWriteUnavailable
	}
	defer root.Close()
	if err = root.MkdirAll(filepath.Dir(local), 0700); err != nil {
		return result, agent.ErrWorkspaceWriteRejected
	}
	temporary := ".oac-write-" + uuid.NewString()
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, agent.ErrWorkspaceWriteRejected
	}
	defer root.Remove(temporary)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return result, agent.ErrWorkspaceWriteRejected
	}
	// Link publishes complete bytes without replacing an existing destination.
	if err = root.Link(temporary, local); err != nil {
		if info, e := root.Lstat(local); e == nil {
			if info.IsDir() {
				return result, agent.ErrWorkspaceWriteDirectory
			}
			return result, agent.ErrWorkspaceWriteUnsafe
		}
		return result, agent.ErrWorkspaceWriteRejected
	}
	return agent.WorkspaceWriteResult{SizeBytes: int64(len(data))}, nil
}

const artifactFileBytes int64 = 200 << 20
const artifactBatchBytes int64 = 500 << 20
const artifactEntries = 4096

type nativeExport struct {
	ctx     context.Context
	root    *os.Root
	archive *tar.Writer
	entries int
	bytes   int64
}

func (b *Binding) exportNativeOutputs(ctx context.Context, output io.Writer) error {
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
	if depth > 64 || len(path) > 4096 {
		return errors.New("workspace export exceeds traversal bound")
	}
	dir, err := openNativePath(x.root, path)
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(artifactEntries + 1)
	_ = dir.Close()
	if err != nil && err != io.EOF {
		return err
	}
	x.entries += len(entries)
	if x.entries > artifactEntries {
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
	if !before.Mode().IsRegular() || size < 0 || size > artifactFileBytes || size > artifactBatchBytes-x.bytes {
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

// ReadWorkspaceFile returns the existing protocol's bounded prefix; the wire
// has no offset field. Non-regular entries cannot become unbounded streams.
func (b *Binding) ReadWorkspaceFile(ctx context.Context, path string, limit int) (agent.WorkspaceReadResult, error) {
	result := agent.WorkspaceReadResult{}
	if b == nil || ctx.Err() != nil {
		return result, agent.ErrWorkspaceReadUnavailable
	}
	if path == "" || limit < 1 || limit > proto.WorkspaceReadMaxBytes {
		return result, agent.ErrWorkspaceReadInvalid
	}
	local, err := nativeAPIPath(path)
	if err != nil {
		return result, agent.ErrWorkspaceReadInvalid
	}
	root, err := os.OpenRoot(b.workspace)
	if err != nil {
		return result, agent.ErrWorkspaceReadUnavailable
	}
	defer root.Close()
	info, err := root.Lstat(local)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, agent.ErrWorkspaceReadInvalid
	}
	file, err := openNativePath(root, local)
	if err != nil {
		return result, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return result, agent.ErrWorkspaceReadUncertain
	}
	if !info.Mode().IsRegular() {
		return result, agent.ErrWorkspaceReadInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(nativeContextReader{ctx: ctx, reader: file}, int64(limit)+1))
	if err != nil {
		return result, agent.ErrWorkspaceReadUncertain
	}
	result.Truncated = len(raw) > limit
	if result.Truncated {
		raw = raw[:limit]
	}
	result.Data = raw
	return result, nil
}
