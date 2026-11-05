package localworkspace

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
	"github.com/google/uuid"
)

func newNativeBinding(environment, workspace, capabilities string) (*Binding, error) {
	if !canonicalID(environment) {
		return nil, errors.New("local workspace requires a canonical Environment identity")
	}
	for _, path := range []string{workspace, capabilities} {
		if runtimefs.ValidateLocalPath(path) != nil || filepath.Dir(path) == path {
			return nil, errors.New("local workspace requires absolute installation paths")
		}
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return nil, errors.New("local workspace root must be an existing directory")
	}
	return &Binding{environment: environment, workspace: workspace, capabilityRoot: capabilities, writer: &fileWriter{}}, nil
}

func canonicalID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

// ReadToolEnvironment reads explicit initialization values for this installation.
func (b *Binding) ReadToolEnvironment() (map[string]string, error) {
	return ReadOptionalToolEnvironment()
}
