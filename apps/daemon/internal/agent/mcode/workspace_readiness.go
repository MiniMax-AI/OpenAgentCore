package mcode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
)

// CheckWorkspace verifies the installed companion. Public qualification remains
// an operator deployment requirement, not an implication of this probe.
func CheckWorkspace(ctx context.Context, c WorkspaceConfig) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	p, err := clirunner.Start(clirunner.StartOptions{Parent: ctx, Binary: c.Node, Args: []string{filepath.Join(filepath.Dir(c.Bridge), "check.mjs")}, Env: executionEnvironment(), OwnProcessGroup: true})
	if err != nil {
		return err
	}
	defer p.Cancel()
	go func() { _, _ = io.Copy(io.Discard, p.Stderr) }()
	raw, err := io.ReadAll(io.LimitReader(p.Stdout, 4097))
	if err != nil || len(raw) > 4096 {
		p.Cancel()
	}
	waitErr := p.Wait()
	if err != nil || waitErr != nil {
		return fmt.Errorf("mcode: workspace companion check failed")
	}
	return ValidateWorkspaceReadiness(raw)
}

// ValidateWorkspaceReadiness checks the installed companion's private contract.
// Neither upstream version alone nor a successful CLI --version proves cleanup.
func ValidateWorkspaceReadiness(raw []byte) error {
	var info struct {
		Protocol       int `json:"protocol"`
		Native, Source string
	}
	if len(raw) > 4096 || json.Unmarshal(raw, &info) != nil || info.Protocol != 2 || info.Native != SupportedVersion || info.Source != "33b259bbbeb1c16433390869938191d09bdb0680" {
		return fmt.Errorf("mcode: workspace companion check failed")
	}
	return nil
}
