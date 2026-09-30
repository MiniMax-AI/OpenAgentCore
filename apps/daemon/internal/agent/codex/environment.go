package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Check the native provider instead of assuming an older binary honors the flag.
func verifyNoExecutionEnvironment(ctx context.Context, rpc *JSONRPCClient) error {
	for _, id := range []string{"local", "remote"} {
		status, err := nativeEnvironmentStatus(ctx, rpc, id)
		if err != nil {
			return fmt.Errorf("codex: cannot confirm disabled execution environment: %w", err)
		}
		if status != "unknown" {
			return fmt.Errorf("codex: execution environment %s was not disabled", id)
		}
	}
	return nil
}

func nativeEnvironmentStatus(ctx context.Context, rpc *JSONRPCClient, id string) (string, error) {
	raw, err := rpc.Request(ctx, "environment/status", map[string]string{"environmentId": id})
	if err != nil {
		return "", err
	}
	var result struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Status == "" {
		return "", fmt.Errorf("codex: invalid native environment status")
	}
	return result.Status, nil
}

// Native still recognizes the retired transport variables. Reject them before
// setup so inherited or operator options cannot select a separate executor.
// The explicit none selector remains part of native execution isolation.
func validateNativeTransportEnvironment(req proto.PromptRequestPayload) error {
	options, err := buildSessionEnv(req.AgentOptions)
	if err != nil {
		return err
	}
	for _, environment := range [][]string{os.Environ(), options} {
		for _, entry := range environment {
			key, value, _ := strings.Cut(entry, "=")
			if value == "" {
				continue
			}
			if (key == "CODEX_EXEC_SERVER_URL" && value != "none") || strings.HasPrefix(key, "CODEX_EXEC_SERVER_NOISE_") {
				return errors.New("codex: retired executor transport configuration is not supported")
			}
		}
	}
	return nil
}
