package e2b

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"time"
)

var ErrRequestUnconfirmed = errors.New("E2B verification could not be confirmed")
var ErrTemplateInvalid = errors.New("E2B template build rejected")
var ErrCredentialInvalid = errors.New("E2B credential rejected")
var ErrTeamMismatch = errors.New("E2B team does not own the retained sandbox deployment")

// VerifyCredential is read-only and bounded. A public readable template alone
// does not prove team ownership. References are one bounded Core-owned page.
func (p *Provider) VerifyCredential(ctx context.Context, refs []sandbox.Reference) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if len(refs) > 32 {
		return sandbox.ErrInvalid
	}
	for _, r := range refs {
		if !validReference(r) {
			return sandbox.ErrInvalid
		}
	}
	deadline, _ := ctx.Deadline()
	out, err := p.caller.Call(ctx, Request{Version: ProtocolVersion, Operation: "verify_credential", Config: p.config, References: refs, Deadline: deadline})
	if err != nil || out.Version != ProtocolVersion {
		return ErrRequestUnconfirmed
	}
	switch out.ErrorCode {
	case "unauthorized":
		return ErrCredentialInvalid
	case "team_mismatch", "invalid":
		return ErrTeamMismatch
	case "":
		if out.DeploymentValid && out.Info == nil && out.Command == nil && out.Observations == nil && out.TemplateBuild == nil {
			return nil
		}
	}
	return ErrRequestUnconfirmed
}
