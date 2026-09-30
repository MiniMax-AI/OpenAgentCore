package dispatch

import (
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func validateExecutionEnvironment(req proto.PromptRequestPayload, caps proto.AgentKindCapabilities) error {
	if err := req.ValidateProgrammaticToolCallingDisable(caps.ProgrammaticToolCallingDisable.IsSupported()); err != nil {
		return err
	}
	if err := req.ValidateToolSearch(caps.ToolSearch.IsSupported()); err != nil {
		return err
	}
	if req.LocalEnvironment != nil && (req.DisableExecutionEnvironment || !caps.LocalEnvironment.IsSupported()) {
		return errors.New("engine does not support this local Environment configuration")
	}
	if req.DisableExecutionEnvironment && !caps.EnvironmentNone.IsSupported() {
		return errors.New("engine does not support execution environment none")
	}
	return validateMCPHTTP(req, caps)
}
