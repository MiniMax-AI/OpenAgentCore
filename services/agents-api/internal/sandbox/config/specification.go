package config

import "errors"

// Local paths belong to the node. Core owns reservation capacity, execution
// resources and the immutable deployment release it enrolled with.
func validateSpecification(c Config) error {
	if c.Provider != "docker" && c.Provider != "microsandbox" {
		return errors.New("nodes support Docker or microsandbox; E2B is managed by Core")
	}
	if c.Generation == 0 {
		return errors.New("node requires a deployment generation; obtain configuration from Core")
	}
	if err := c.Specification.Validate(c.Provider); err != nil {
		return err
	}
	r := c.Specification.Runtime
	if d := c.Docker; d != nil {
		if d.Image != r.ImageID && d.Image != r.ImageManifestDigest {
			return errors.New("Docker Runtime image differs from the deployment release")
		}
	}
	if m := c.Microsandbox; m != nil {
		s := c.Specification.Resources
		if uint32(m.CPUs) != s.CPUs || m.MemoryMiB != s.MemoryMiB || m.RootDiskMiB != s.RootDiskMiB || m.EnvironmentDiskMiB != s.EnvironmentDiskMiB {
			return errors.New("microsandbox CPU, memory or disk limits differ from the deployment specification")
		}
		if m.Image != r.MicrosandboxRef || m.RuntimeSHA256 != r.RuntimeSHA256 || m.FirmwareSHA256 != r.FirmwareSHA256 {
			return errors.New("microsandbox Runtime or firmware differs from the deployment release")
		}
	}
	return nil
}
