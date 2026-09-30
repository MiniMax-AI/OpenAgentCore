package e2b

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

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
		return sandbox.ErrConfigurationUnconfirmed
	}
	switch out.ErrorCode {
	case "unauthorized":
		return sandbox.ErrCredentialRejected
	case "team_mismatch", "invalid":
		return sandbox.ErrCredentialOwnership
	case "":
		if out.DeploymentValid && out.Info == nil && out.Command == nil && out.Observations == nil && out.TemplateBuild == nil {
			return nil
		}
	}
	return sandbox.ErrConfigurationUnconfirmed
}
