package agentcapabilities

import (
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

// The rest of this file applies the installation rules to a local directory,
// as an os.Root.

// DirectoryResolver opens a declared source after local authorization and path
// checks. Finalize owns and closes each returned root; callers retain no handle.
type DirectoryResolver func(string) (*os.Root, error)

// InstallSkill stages a Skill archive in root.
func InstallSkill(root *os.Root, archive []byte, metadata agentskill.Metadata) error {
	tree, err := SkillTree(archive, metadata)
	if err != nil || unfinished(root) != nil {
		return ErrInvalid
	}
	return writeTree(root, tree.Root, tree.Files)
}

// InstallPlugin stages a Plugin archive in slot of root.
func InstallPlugin(root *os.Root, slot int, archive []byte, metadata agentplugin.Metadata) error {
	tree, err := PluginTree(slot, archive, metadata)
	if err != nil || unfinished(root) != nil {
		return ErrInvalid
	}
	return writeTree(root, tree.Root, tree.Files)
}

// Finalize completes the installation in installed with a Snapshot,
// resolving each declared directory with resolve.
func Finalize(installed *os.Root, input Input, identity Identity, resolve DirectoryResolver) error {
	if len(input.Directories) > 0 && resolve == nil {
		return ErrInvalid
	}
	if _, err := installed.Lstat(ManifestName); !os.IsNotExist(err) {
		return ErrInvalid
	}
	snapshot, err := NewSnapshot(input, identity, func(name string) ([]fs.DirEntry, error) { return readDir(installed, name) },
		func(name string) ([]agentbundle.File, error) { return ReadTree(installed, name, true) })
	if err != nil {
		return err
	}
	for _, source := range input.Directories {
		directory, err := resolve(source)
		if err != nil || directory == nil {
			if directory != nil {
				directory.Close()
			}
			return ErrInvalid
		}
		files, err := ReadTree(directory, ".", false)
		if closeErr := directory.Close(); err != nil || closeErr != nil {
			return ErrInvalid
		}
		tree, err := snapshot.Capture(files)
		if err != nil || writeTree(installed, tree.Root, tree.Files) != nil {
			return ErrInvalid
		}
	}
	body, err := snapshot.Manifest()
	if err != nil || writeFile(installed, ManifestName+".tmp", body, 0400) != nil || installed.Rename(ManifestName+".tmp", ManifestName) != nil {
		return ErrInvalid
	}
	return syncDirectory(installed, ".")
}

// Load checks the installation in root, as Check does.
func Load(root *os.Root) (Manifest, error) {
	info, err := root.Lstat(ManifestName)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 || info.Size() > MaxManifestBytes {
		return Manifest{}, ErrInvalid
	}
	body, err := root.ReadFile(ManifestName)
	if err != nil {
		return Manifest{}, ErrInvalid
	}
	return Check(body, func(name string) ([]agentbundle.File, error) { return ReadTree(root, name, true) })
}

func readDir(root *os.Root, name string) ([]fs.DirEntry, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, ErrInvalid
	}
	entries, err := file.ReadDir(-1)
	if closeErr := file.Close(); err != nil || closeErr != nil {
		return nil, ErrInvalid
	}
	return entries, nil
}

func unfinished(root *os.Root) error {
	for _, name := range []string{ManifestName, ManifestName + ".tmp"} {
		if _, err := root.Lstat(name); !os.IsNotExist(err) {
			return ErrInvalid
		}
	}
	return nil
}

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
	if !validRelative(name) || makeDirectories(root, path.Dir(name)) != nil || root.Mkdir(name, 0700) != nil {
		return ErrInvalid
	}
	directory, err := root.OpenRoot(name)
	if err != nil {
		return ErrInvalid
	}
	defer directory.Close()
	for _, file := range files {
		if !validRelative(file.Path) || makeDirectories(directory, path.Dir(file.Path)) != nil {
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
	directory, err := root.OpenRoot(name)
	if err != nil {
		return ErrInvalid
	}
	defer directory.Close()
	if runtimefs.SyncDirectory(directory) != nil {
		return ErrInvalid
	}
	return nil
}

// Create only real directory parents. os.Root prevents escapes, while these
// checks also reject aliases that remain inside the protected installation.
func makeDirectories(root *os.Root, name string) error {
	if name == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(name, "/") {
		current = path.Join(current, component)
		if err := root.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
			return ErrInvalid
		}
		info, err := root.Lstat(current)
		if err != nil || !info.IsDir() {
			return ErrInvalid
		}
	}
	return nil
}
