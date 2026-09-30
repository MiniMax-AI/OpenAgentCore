package adminaudit

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// ErrInvalidSource reports an administrator mutation record that cannot be
// recorded: missing or malformed provenance, or a malformed action or resource.
// The mutation it belongs to fails closed.
var ErrInvalidSource = errors.New("invalid administrator audit source")

// ValidateProjectMutation checks the record of a mutation in the Project that
// s names.
func (s Source) ValidateProjectMutation(action, resourceType, resourceID string) error {
	if !writeaudit.ValidText(s.ProjectID, 128, true) {
		return ErrInvalidSource
	}
	return s.validate(action, resourceType, resourceID)
}

// ValidateDeploymentMutation checks the record of a deployment-wide mutation,
// which has no Project, so s names none.
func (s Source) ValidateDeploymentMutation(action, resourceType, resourceID string) error {
	if s.ProjectID != "" {
		return ErrInvalidSource
	}
	return s.validate(action, resourceType, resourceID)
}

func (s Source) validate(action, resourceType, resourceID string) error {
	if !writeaudit.ValidText(s.CredentialID, 64, true) || !writeaudit.ValidText(s.ActorLabel, 128, false) || !writeaudit.ValidText(s.RequestID, 128, true) || !writeaudit.ValidText(s.TraceID, 128, true) ||
		!writeaudit.ValidText(action, 64, true) || !writeaudit.ValidText(resourceType, 64, true) || !writeaudit.ValidText(resourceID, 256, true) {
		return ErrInvalidSource
	}
	return nil
}
