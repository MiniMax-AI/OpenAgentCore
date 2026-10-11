package credentialcrypto

import (
	"bytes"
	"testing"
)

func TestWorkspaceConfigurationCredentialBinding(t *testing.T) {
	c, err := New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := c.SealWorkspaceConfiguration([]byte("private-key"), "installation", "configuration")
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range [][2]string{{"other", "configuration"}, {"installation", "other"}, {"configuration", "installation"}} {
		if _, err := c.OpenWorkspaceConfiguration(encrypted, binding[0], binding[1]); err == nil {
			t.Fatal("accepted different workspace configuration binding")
		}
	}
	if _, err := c.OpenSandboxDeployment(encrypted, "installation", 1); err == nil {
		t.Fatal("accepted a workspace credential as a sandbox credential")
	}
	if _, err := c.SealWorkspaceConfiguration([]byte("x"), "", "configuration"); err == nil {
		t.Fatal("sealed without an installation")
	}
	plain, err := c.OpenWorkspaceConfiguration(encrypted, "installation", "configuration")
	if err != nil || string(plain) != "private-key" {
		t.Fatal("credential did not roundtrip", err)
	}
}
