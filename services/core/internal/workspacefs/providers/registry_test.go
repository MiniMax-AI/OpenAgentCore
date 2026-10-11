package providers

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

func TestRegistryRoles(t *testing.T) {
	r := New()
	quota := json.RawMessage(`{"root":"/nfs/workspaces","namespace_id":"22222222-2222-4222-8222-222222222222","uid":65532,"quota":{"endpoint":"storage.internal:22","user":"oac-quota","host_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKEJUu1hJdEp2CdwRXbFxsHlMQpWSKMKjvP3w1Frd3gk"}}`)
	config := workspacefs.Configuration{ID: "11111111-1111-4111-8111-111111111111", Adapter: "nfs", Parameters: quota}
	if _, err := r.Normalize(config, nil); !errors.Is(err, workspacefs.ErrInvalid) {
		t.Fatal("quota configuration normalized without its credential", err)
	}
	if _, err := r.Control(workspacefs.ConfigurationRecord{Configuration: config}); !errors.Is(err, workspacefs.ErrInvalid) {
		t.Fatal("credentialed Control constructed without credential", err)
	}
	// Nodes resolve with public configuration only; ownership checks follow.
	binding := workspacefs.Binding{Configuration: config, Attachment: workspacefs.Attachment{Reference: workspacefs.Reference{TenantID: "33333333-3333-4333-8333-333333333333", EnvironmentID: "44444444-4444-4444-8444-444444444444", ObjectID: "55555555-5555-4555-8555-555555555555"},
		ConfigurationID: config.ID, Kind: workspacefs.AttachmentHostDirectory, Native: json.RawMessage(`{"namespace_id":"00000000-0000-4000-8000-000000000000"}`)}}
	if _, err := r.Resolve(t.Context(), binding); !errors.Is(err, workspacefs.ErrOwnership) {
		t.Fatal("resolver did not validate the public binding", err)
	}
	config.Adapter = "other"
	if _, err := r.Normalize(config, nil); !errors.Is(err, workspacefs.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := r.Control(workspacefs.ConfigurationRecord{Configuration: config}); !errors.Is(err, workspacefs.ErrUnsupported) {
		t.Fatal(err)
	}
}
