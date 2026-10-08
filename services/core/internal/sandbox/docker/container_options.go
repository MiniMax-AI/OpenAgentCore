package docker

import (
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

// runtimeContainerOptions is the allocation's container: the Sandbox I/O
// service as its only process, reading the input that bootstrap writes.
func runtimeContainerOptions(config Config, name string, labels map[string]string) client.ContainerCreateOptions {
	limit := int64(128)
	memory, cpus := int64(2*1024*1024*1024), int64(2*1000000000)
	if config.Resources != nil {
		memory = int64(config.Resources.MemoryMiB) * 1024 * 1024
		cpus = int64(config.Resources.CPUs) * 1000000000
	}
	return client.ContainerCreateOptions{Name: name, Image: config.Image,
		Config: &container.Config{User: "1000:1000", WorkingDir: "/environment/workspace", Labels: labels,
			Entrypoint: []string{"/usr/local/bin/oac-sandbox-io", "--bootstrap-file", "/home/runtime/sandbox-io-bootstrap.json"}},
		HostConfig: &container.HostConfig{ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges", "seccomp=" + config.Seccomp, "apparmor=unconfined"}, NetworkMode: container.NetworkMode(config.Network),
			Resources: container.Resources{PidsLimit: &limit, Memory: memory, NanoCPUs: cpus}, Tmpfs: map[string]string{"/tmp": "rw,nosuid,nodev,size=128m"},
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: name + "-home", Target: "/home"},
				{Type: mount.TypeVolume, Source: name + "-environment", Target: "/environment"},
				// The workspace is also at its public path.
				{Type: mount.TypeVolume, Source: name + "-environment", Target: "/workspace", VolumeOptions: &mount.VolumeOptions{Subpath: "workspace", NoCopy: true}},
			},
		},
	}
}
