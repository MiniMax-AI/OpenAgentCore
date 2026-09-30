//go:build !linux

package cli

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"os"
)

const suspendControlEnv = "OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE"

type suspendIdentity struct {
	EnvironmentID string
	SuspendID     string
}
type suspendControl struct {
	identity    suspendIdentity
	signal      chan os.Signal
	lastResumed *proto.EnvironmentSuspendPayload
}

func newSuspendControl() (*suspendControl, error) {
	if os.Getenv(suspendControlEnv) != "" {
		return nil, errors.New("hosted suspension requires Linux")
	}
	return nil, nil
}
func (c *suspendControl) Arm(proto.EnvironmentSuspendPayload) error {
	return errors.New("hosted suspension requires Linux")
}
func (c *suspendControl) Wait(context.Context) error {
	return errors.New("hosted suspension requires Linux")
}
func (c *suspendControl) Disarm() error     { return errors.New("hosted suspension requires Linux") }
func (c *suspendControl) Close()            {}
func runResume(*runContext, []string) error { return errors.New("hosted suspension requires Linux") }
