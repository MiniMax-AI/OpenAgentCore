package cli

import (
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/auth"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimebootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

// The launch file is the sole credential source for this connection. Reopening
// it on process restart neither pairs again nor overwrites an auth profile.
func bootstrapProfile(path string) (*auth.Profile, error) {
	raw, err := runtimefs.ReadPrivatePath(path, runtimebootstrap.MaxBytes)
	if err != nil {
		return nil, errors.New("connect: Runtime bootstrap file unavailable")
	}
	input, err := runtimebootstrap.Decode(raw)
	if err != nil {
		return nil, err
	}
	return &auth.Profile{ServerURL: input.CoreURL, RuntimeID: input.DeviceID, RunnerCredential: input.Credential}, nil
}
