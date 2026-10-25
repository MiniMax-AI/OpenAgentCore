package cli

import (
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/auth"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimebootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

// The launch file is the sole credential source for this connection. Reopening
// it on process restart never reads or overwrites an auth profile.
func bootstrapProfile(path string) (*auth.Profile, bool, error) {
	raw, err := runtimefs.ReadPrivatePath(path, runtimebootstrap.MaxBytes)
	if err != nil {
		return nil, false, errors.New("connect: Runtime bootstrap file unavailable")
	}
	input, err := runtimebootstrap.Decode(raw)
	if err != nil {
		return nil, false, err
	}
	if err = localworkspace.Bind(input.EnvironmentID, input.SessionID, input.Workspace, localworkspace.CapabilityDirectory); err != nil {
		return nil, false, errors.New("connect: Runtime workspace binding unavailable")
	}
	return &auth.Profile{ServerURL: input.CoreURL, RuntimeID: input.DeviceID, RunnerCredential: input.Credential}, input.SuspendControl, nil
}
