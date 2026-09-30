package claudesdk

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

type runtimeCheckCache struct {
	mu    sync.Mutex
	stamp string
	info  RuntimeInfo
}

// Registration retains the package check. Replacing an installed entrypoint,
// readiness companion, package manifest or Node binary invalidates the result.
func (c *runtimeCheckCache) check(ctx context.Context, config Config) (RuntimeInfo, error) {
	node := config.Node
	if node == "" {
		node = "node"
	}
	node, err := exec.LookPath(node)
	if err != nil {
		return RuntimeInfo{}, err
	}
	root := filepath.Dir(config.Entrypoint)
	stamp := ""
	for _, path := range []string{node, config.Entrypoint, filepath.Join(root, "runtime_check.js"), filepath.Join(root, "../package.json"), filepath.Join(root, "../runtime-manifest.json")} {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				stamp += path + ":missing;"
				continue
			}
			return RuntimeInfo{}, err
		}
		stamp += fmt.Sprintf("%s:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stamp == stamp && c.info.Protocol == 3 {
		return c.info, nil
	}
	info, err := CheckRuntime(ctx, config)
	if err == nil {
		c.info, c.stamp = info, stamp
	}
	return info, err
}
