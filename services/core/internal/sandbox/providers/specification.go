package providers

import (
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// Local paths belong to the node. Core owns reservation capacity, execution
// resources and the immutable deployment release it enrolled with.
func (r *Registry) validateSpecification(c Config) error {
	adapter, err := r.Lookup(c.Provider)
	if err != nil {
		return err
	}
	if adapter.Mode != "nodes" {
		return fmt.Errorf("%w: selected provider does not support node hosting", sandbox.ErrInvalid)
	}
	if c.Generation == 0 {
		return errors.New("node requires a deployment generation; obtain configuration from Core")
	}
	if err := r.ValidateSpecification(c.Provider, c.Specification); err != nil {
		return err
	}
	release := c.Specification.Runtime
	if d := c.Docker; d != nil {
		if d.Image != release.ImageID && d.Image != release.ImageManifestDigest {
			return errors.New("Docker Runtime image differs from the deployment release")
		}
	}
	if m := c.Microsandbox; m != nil {
		s := c.Specification.Resources
		if uint32(m.CPUs) != s.CPUs || m.MemoryMiB != s.MemoryMiB || m.RootDiskMiB != s.RootDiskMiB || m.EnvironmentDiskMiB != s.EnvironmentDiskMiB {
			return errors.New("microsandbox CPU, memory or disk limits differ from the deployment specification")
		}
		if m.Image != release.MicrosandboxRef || m.RuntimeSHA256 != release.RuntimeSHA256 || m.FirmwareSHA256 != release.FirmwareSHA256 {
			return errors.New("microsandbox Runtime or firmware differs from the deployment release")
		}
	}
	return nil
}
