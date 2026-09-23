package docker

import (
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

// runtimeContainerOptions is shared by managed and user-owned V1 Runtime launch.
// Keep isolation and volume layout identical; only bootstrap authority differs.
func runtimeContainerOptions(config Config, name string, labels map[string]string, environment []string) client.ContainerCreateOptions {
	limit := int64(128)
	// A nested native sandbox must mount its own procfs. Keep sysfs secrets
	// masked; only this qualified image profile opts out of Docker's proc masks.
	var masked, readonly []string
	var init *bool
	if config.NestedSandbox {
		masked = []string{"/sys/firmware", "/sys/devices/virtual/powercap"}
		readonly = []string{}
		enabled := true
		init = &enabled
	}
	return client.ContainerCreateOptions{Name: name, Image: config.Image,
		Config: &container.Config{User: "1000:1000", WorkingDir: "/environment/workspace", Labels: labels, Env: environment},
		HostConfig: &container.HostConfig{ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges", "seccomp=" + config.Seccomp, "apparmor=unconfined"}, NetworkMode: container.NetworkMode(config.Network), ExtraHosts: config.ExtraHosts,
			MaskedPaths: masked, ReadonlyPaths: readonly, Init: init,
			Resources: container.Resources{PidsLimit: &limit, Memory: 2 * 1024 * 1024 * 1024, NanoCPUs: 2 * 1000000000}, Tmpfs: map[string]string{"/tmp": "rw,nosuid,nodev,size=128m"},
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: name + "-home", Target: "/home"},
				{Type: mount.TypeVolume, Source: name + "-environment", Target: "/environment"},
				// The native sandbox mounts canonical roots, omitting symlink aliases.
				// Expose the same workspace at its public path; trusted atomic staging
				// remains entirely on the original /environment mount.
				{Type: mount.TypeVolume, Source: name + "-environment", Target: "/workspace", VolumeOptions: &mount.VolumeOptions{Subpath: "workspace", NoCopy: true}},
			},
		},
	}
}
