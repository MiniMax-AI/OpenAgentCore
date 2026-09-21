package codex

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
)

const nativeManagedRequirements = "/etc/codex/requirements.toml"

// Preserve the image's native filesystem, approval and hook requirements. This
// adapter adds the exact network ceiling; Core never supplies native TOML.
func managedNetworkRequirements(base []byte, policy agentnetwork.Policy) ([]byte, error) {
	if policy.Access != "restricted" || policy.Validate() != nil {
		return nil, errors.New("codex: invalid restricted network policy")
	}
	var config map[string]any
	if _, err := toml.Decode(string(base), &config); err != nil {
		return nil, err
	}
	if _, exists := config["experimental_network"]; exists {
		return nil, errors.New("codex: image already defines managed network requirements")
	}
	domains := make(map[string]string)
	for _, host := range policy.Hosts() {
		domains[host] = "allow"
	}
	var addition bytes.Buffer
	err := toml.NewEncoder(&addition).Encode(map[string]any{"experimental_network": map[string]any{
		"enabled": true, "managed_allowed_domains_only": true,
		"allow_upstream_proxy": false, "dangerously_allow_all_unix_sockets": false,
		"allow_local_binding": false, "domains": domains,
	}})
	if err != nil {
		return nil, err
	}
	result := append(append([]byte{}, base...), '\n')
	return append(result, addition.Bytes()...), nil
}

func prepareManagedNetwork(plan *SessionPlan, policy agentnetwork.Policy) error {
	var home string
	for _, entry := range plan.Env {
		if value, found := strings.CutPrefix(entry, "CODEX_HOME="); found {
			home = value
		}
	}
	if !filepath.IsAbs(home) {
		return errors.New("codex: managed network requires private Session state")
	}
	base, err := os.ReadFile(nativeManagedRequirements)
	if err != nil {
		return err
	}
	contents, err := managedNetworkRequirements(base, policy)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(home, ".managed-network-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(contents); err == nil {
		err = file.Chmod(0400)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	path := filepath.Join(home, "managed-network.toml")
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	// Retain this non-secret file with Session state. Rebuild it from the frozen
	// policy on preparation; cleanup never races a process still holding its mount.
	plan.managedRequirements = path
	plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"features.network_proxy", "true"})
	return nil
}

// The existing RPC client owns the sole child, its pipes and teardown. The PID
// namespace terminates native descendants when that owned process is killed.
func configureManagedNetworkProcess(cfg *JSONRPCConfig, requirements string) error {
	if requirements == "" {
		return nil
	}
	if !filepath.IsAbs(requirements) {
		return errors.New("codex: managed requirements path must be absolute")
	}
	binary, err := exec.LookPath(cfg.Binary)
	if err != nil {
		return err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return err
	}
	cfg.Binary = "/usr/bin/bwrap"
	cfg.ExtraArgs = append([]string{"--die-with-parent", "--unshare-pid", "--bind", "/", "/",
		"--dev-bind", "/dev", "/dev", "--proc", "/proc", "--ro-bind", requirements,
		nativeManagedRequirements, binary}, cfg.ExtraArgs...)
	return nil
}
