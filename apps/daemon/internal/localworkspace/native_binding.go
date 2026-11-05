package localworkspace

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
	"github.com/google/uuid"
)

func newNativeBinding(environment, workspace, capabilities string) (*Binding, error) {
	if id, err := uuid.Parse(environment); err != nil || id == uuid.Nil || id.String() != environment {
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

// ReadToolEnvironment reads explicit initialization values for this installation.
func (b *Binding) ReadToolEnvironment() (map[string]string, error) {
	return ReadOptionalToolEnvironment()
}
