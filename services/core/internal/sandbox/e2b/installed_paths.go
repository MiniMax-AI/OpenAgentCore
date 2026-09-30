package e2b

import (
	"fmt"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// InstalledPaths resolves the adapter's fixed distribution layout.
func InstalledPaths(paths sandbox.ProcessPaths) (string, string, error) {
	if !filepath.IsAbs(paths.ArtifactRoot) || !filepath.IsAbs(paths.StateRoot) {
		return "", "", fmt.Errorf("%w: provider artifact and state roots must be absolute", sandbox.ErrInvalid)
	}
	return filepath.Join(paths.ArtifactRoot, "e2b", "oac-e2b-provider"), filepath.Join(paths.StateRoot, "e2b"), nil
}
