package store

import (
	"bytes"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/google/uuid"
)

func TestStoredSystemPackagesRejected(t *testing.T) {
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(nil, cipher)
	tenant, session := uuid.NewString(), uuid.NewString()
	for _, value := range []string{`null`, `[]`, `["jq"]`} {
		packages := []byte(`{"npm":[],"python":[],"system":` + value + "}")
		plain := append([]byte(`{"packages":`), packages...)
		plain = append(plain, '}')
		encrypted, err := cipher.SealEnvironmentSetup(plain, credentialcrypto.EnvironmentSetupBinding{TenantID: tenant, Resource: "session", OwnerID: session, Field: "initialization"})
		if err != nil {
			t.Fatal(err)
		}
		var output environmentconfig.Setup
		if err := s.openEnvironmentSetup(tenant, "session", session, "initialization", encrypted, &output); err == nil {
			t.Fatal("snapshot silently ignored removed system packages", value)
		}
	}
}
