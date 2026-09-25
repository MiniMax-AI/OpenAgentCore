package credentialcrypto

import (
	"bytes"
	"testing"
)

func TestDeploymentModelProviderBinding(t *testing.T) {
	c, err := New(bytes.Repeat([]byte{29}, 32))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("deployment-provider-canary")
	encrypted, err := c.SealDeploymentModelProvider(raw, "codex")
	if err != nil || bytes.Contains(encrypted, raw) {
		t.Fatal("credential encryption failed")
	}
	if opened, err := c.OpenDeploymentModelProvider(encrypted, "codex"); err != nil || !bytes.Equal(raw, opened) {
		t.Fatal("credential round trip failed")
	}
	if _, err := c.OpenDeploymentModelProvider(encrypted, "mcode"); err == nil {
		t.Fatal("another harness opened the ciphertext")
	}
	if _, err := c.OpenAgentModelExecution(encrypted, "codex", "codex"); err == nil {
		t.Fatal("deployment ciphertext accepted for Agent purpose")
	}
	if _, err := c.OpenModelExecution(encrypted, "codex", "codex"); err == nil {
		t.Fatal("deployment ciphertext accepted for Session purpose")
	}
	session, err := c.SealModelExecution(raw, "codex", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenDeploymentModelProvider(session, "codex"); err == nil {
		t.Fatal("Session ciphertext accepted for deployment purpose")
	}
}
