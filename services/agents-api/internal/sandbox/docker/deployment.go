package docker

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/moby/moby/client"
)

// Resource drift blocks managed readiness, but never ownership-based cleanup.
// Resolve the immutable selector through Docker because classic and containerd
// stores may identify the same exported image by different digests.
func (p *Provider) verifyConfiguration(ctx context.Context, actual client.ContainerInspectResult) error {
	if p.config.Resources == nil {
		return nil
	}
	resources := p.config.Resources
	config := actual.Container.HostConfig
	if config == nil || config.NanoCPUs != int64(resources.CPUs)*1000000000 || config.Memory != int64(resources.MemoryMiB)*1024*1024 {
		return sandbox.ErrInvalid
	}
	image, err := p.client.ImageInspect(ctx, p.config.Image)
	if err != nil {
		return err
	}
	if image.ID == "" || actual.Container.Image != image.ID {
		return sandbox.ErrInvalid
	}
	return nil
}
