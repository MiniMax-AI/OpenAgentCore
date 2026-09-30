package cli

import (
	"context"
	"errors"
	"fmt"

	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/claudesdk"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/codex"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/installroot"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/mcode"
)

func nativeExe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
func nativeNode(root string) string {
	base := nativeComponentRoot(root, "node")
	if runtime.GOOS != "windows" {
		base = filepath.Join(base, "bin")
	}
	return filepath.Join(base, nativeExe("node"))
}

func nativeHarnessEnvironment(root string, selected []string) map[string]string {
	node := nativeNode(root)
	values := map[string]string{"PATH": filepath.Dir(node) + string(os.PathListSeparator) + os.Getenv("PATH")}
	for _, name := range selected {
		dir := nativeComponentRoot(root, name)
		values["PATH"] = filepath.Join(dir, "bin") + string(os.PathListSeparator) + values["PATH"]
		for key, value := range nativeHarnesses[name].Environment(dir, node) {
			values[key] = value
		}

	}
	return values
}

func withNativeEnv(values map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		// Windows environment names are case-insensitive.
		replaced := false
		for k := range values {
			if strings.EqualFold(k, key) {
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, entry)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

// Installation registration is local to Runtime; Core does not see native paths.
var nativeHarnesses = map[string]agent.Installation{
	"codex": codex.Installation(), "claude": claudesdk.Installation(), "minimax": mcode.Installation(),
}
var probeNativeInstallation = checkNativeInstallation

func checkNativeInstallation(ctx context.Context, root string, selected []string) error {
	env := withNativeEnv(nativeHarnessEnvironment(root, selected))
	node := nativeNode(root)
	version, err := installroot.Probe(ctx, node, []string{"--version"}, env, root)
	if err != nil || version != "v"+nativePins["node"] {
		return errors.New("install: bundled Node is unavailable or incompatible; use the matching native distribution")
	}
	for _, name := range selected {
		spec, ok := nativeHarnesses[name]
		if !ok || !spec.Supported() {
			return fmt.Errorf("install: %s is unsupported on this platform", name)
		}
		if err = spec.Check(ctx, nativeComponentRoot(root, name), node, env); err != nil {
			return fmt.Errorf("install: %s; no installed files were replaced", err)
		}
	}
	return nil
}

// Installed discovery is confined to verified adapters; ordinary tool PATH
// remains available to the selected Harness and its tools.
func nativeInstallationKinds(selected []string) map[string]bool {
	kinds := make(map[string]bool, len(selected))
	for _, name := range selected {
		kinds[nativeHarnesses[name].AgentKind] = true
	}
	return kinds
}
