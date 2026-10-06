package deploymentpg_test

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
)

// A missing key is credentialcrypto.ErrUnavailable, and a credential sealed
// with another binding is a decryption failure, never a missing key. Node
// transactions and snapshots never open the credential, so they need no key.
func TestStoredCredentialNeedsTheKeyAndItsBinding(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	id, view := f.initialize(t, changes, setupE2BSelection())
	keyless := deploymentpg.New(pgunit.NewPool(f.pool), nil)
	service := newService(t, keyless, fixturePublicURL)
	if _, err := service.Setup(t.Context()); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("a keyless Store opened the credential", err)
	}
	withoutCredential := func(reads deployment.NodeReads) error {
		d, err := reads.LoadDeployment()
		if err == nil && (!d.CredentialStored || d.CredentialError != nil || d.Configuration.Secret != nil) {
			t.Error("a node read opened the credential", d.CredentialError)
		}
		return err
	}
	if err := keyless.WithNodes(t.Context(), func(tx deployment.NodeTx) error { return withoutCredential(tx) }); err != nil {
		t.Fatal(err)
	}
	if err := keyless.ReadNodes(t.Context(), withoutCredential); err != nil {
		t.Fatal(err)
	}
	sealed, err := f.cipher.SealSandboxDeployment([]byte("fixture-private-api-key"), id, view.Generation+1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE runtime_deployment SET provider_credential=$1", sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Setup(t.Context()); err == nil || err.Error() != "sandbox deployment credential decryption failed" || errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("a wrong binding was not a decryption failure", err)
	}
}
