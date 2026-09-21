package agentcapabilities

import (
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentbundle"
)

// ReadTree stays inside an already-owned root and rejects aliases/special files.
// Installation sources may be writable; retained snapshots must be read-only.
func ReadTree(root *os.Root, name string, immutable bool) ([]agentbundle.File, error) {
	if name != "." && !validRelative(name) {
		return nil, ErrInvalid
	}
	info, err := root.Lstat(name)
	if err != nil || !info.IsDir() {
		return nil, ErrInvalid
	}
	var files []agentbundle.File
	total := 0
	entries := 0
	err = fs.WalkDir(root.FS(), name, func(current string, entry fs.DirEntry, walkErr error) error {
		entries++
		if walkErr != nil || entries > agentbundle.MaxFiles {
			return ErrInvalid
		}
		info, err := root.Lstat(current)
		if err != nil {
			return ErrInvalid
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() || (immutable && info.Mode().Perm()&0222 != 0) || len(files) >= agentbundle.MaxFiles || info.Size() > int64(agentbundle.MaxExpandedBytes-total) {
			return ErrInvalid
		}
		file, err := root.Open(current)
		if err != nil {
			return ErrInvalid
		}
		actual, statErr := file.Stat()
		if statErr != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
			file.Close()
			return ErrInvalid
		}
		body, readErr := io.ReadAll(io.LimitReader(file, int64(agentbundle.MaxExpandedBytes-total)+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(body) > agentbundle.MaxExpandedBytes-total || int64(len(body)) != actual.Size() {
			return ErrInvalid
		}
		total += len(body)
		relative := current
		if name != "." {
			relative = strings.TrimPrefix(current, name+"/")
		}
		if !validRelative(relative) {
			return ErrInvalid
		}
		files = append(files, agentbundle.File{Path: relative, Data: body, Executable: actual.Mode().Perm()&0111 != 0})
		return nil
	})
	if err != nil {
		return nil, ErrInvalid
	}
	return files, nil
}

// writeTree only creates a fresh owned destination. Uncertain writes are never
// retried here; the existing Core initialization owner decides cleanup.
func writeTree(root *os.Root, name string, files []agentbundle.File) error {
	if !validRelative(name) || root.MkdirAll(path.Dir(name), 0700) != nil || root.Mkdir(name, 0700) != nil {
		return ErrInvalid
	}
	directory, err := root.OpenRoot(name)
	if err != nil {
		return ErrInvalid
	}
	defer directory.Close()
	for _, file := range files {
		if !validRelative(file.Path) || directory.MkdirAll(path.Dir(file.Path), 0700) != nil {
			return ErrInvalid
		}
		mode := fs.FileMode(0400)
		if file.Executable {
			mode = 0500
		}
		if err := writeFile(directory, file.Path, file.Data, mode); err != nil {
			return err
		}
	}
	err = fs.WalkDir(directory.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return ErrInvalid
		}
		if entry.IsDir() {
			return syncDirectory(directory, name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return syncDirectory(root, path.Dir(name))
}

func writeFile(root *os.Root, name string, body []byte, mode fs.FileMode) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return ErrInvalid
	}
	_, err = file.Write(body)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ErrInvalid
	}
	return nil
}

func syncDirectory(root *os.Root, name string) error {
	file, err := root.Open(name)
	if err != nil {
		return ErrInvalid
	}
	err = file.Sync()
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ErrInvalid
	}
	return nil
}
