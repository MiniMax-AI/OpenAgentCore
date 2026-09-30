package localworkspace

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
	"github.com/google/uuid"
)

// snapshotMarker remembers completion outside the installed tree. It contains
// only the operator root, never capability configuration or a second inventory.
type snapshotMarker struct {
	directory *os.Root
	unlock    func()
	name      string
	root      string
	completed bool
}

func (b *Binding) openSnapshotMarker() (*snapshotMarker, error) {
	identity := b.capabilityIdentity()
	for _, id := range []string{identity.EnvironmentID, identity.SessionID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return nil, agentcapabilities.ErrInvalid
		}
	}
	private, err := paths.Root()
	if err != nil || agentcapabilities.ValidateLocalDirectories([]string{private}) != nil {
		return nil, agentcapabilities.ErrInvalid
	}
	current, err := os.OpenRoot(private)
	if err != nil {
		return nil, agentcapabilities.ErrInvalid
	}
	for _, child := range []string{"daemon", "capability-installations"} {
		if !privateSnapshotDirectory(current) {
			current.Close()
			return nil, agentcapabilities.ErrInvalid
		}
		if err = runtimefs.MkdirPrivate(current, child); err != nil || runtimefs.SyncDirectory(current) != nil {
			current.Close()
			return nil, agentcapabilities.ErrInvalid
		}
		next, err := current.OpenRoot(child)
		current.Close()
		if err != nil {
			return nil, agentcapabilities.ErrInvalid
		}
		current = next
	}
	if !privateSnapshotDirectory(current) {
		current.Close()
		return nil, agentcapabilities.ErrInvalid
	}
	unlock, err := runtimefs.LockDirectory(current)
	if err != nil {
		current.Close()
		return nil, agentcapabilities.ErrInvalid
	}
	marker := &snapshotMarker{directory: current, unlock: unlock, name: identity.EnvironmentID + "-" + identity.SessionID + ".json", root: b.capabilityRoot}
	completed, err := marker.read()
	if err != nil {
		marker.close()
		return nil, agentcapabilities.ErrInvalid
	}
	marker.completed = completed
	return marker, nil
}

func privateSnapshotDirectory(root *os.Root) bool { return runtimefs.PrivateDirectory(root) == nil }

func (m *snapshotMarker) read() (bool, error) {
	file, err := runtimefs.OpenPrivate(m.directory, m.name, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() > 8192 {
		return false, agentcapabilities.ErrInvalid
	}
	// Exact bytes also reject duplicate members, trailing data and extra fields.
	expected, _ := json.Marshal(struct {
		Root string `json:"capability_root"`
	}{m.root})
	actual, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || string(actual) != string(expected)+"\n" {
		return false, agentcapabilities.ErrInvalid
	}
	return true, nil
}

func (m *snapshotMarker) complete() error {
	if m.completed {
		return nil
	}
	data, err := json.Marshal(struct {
		Root string `json:"capability_root"`
	}{m.root})
	if err != nil {
		return agentcapabilities.ErrInvalid
	}
	file, err := runtimefs.OpenPrivate(m.directory, m.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return agentcapabilities.ErrInvalid
	}
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || runtimefs.SyncDirectory(m.directory) != nil {
		return agentcapabilities.ErrInvalid
	}
	m.completed = true
	return nil
}

func (m *snapshotMarker) close() {
	m.unlock()
	m.directory.Close()
}
