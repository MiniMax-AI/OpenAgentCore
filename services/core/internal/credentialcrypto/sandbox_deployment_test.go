package credentialcrypto

import (
	"bytes"
	"testing"
)

func TestSandboxDeploymentCredentialBinding(t *testing.T) {
	c, err := New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := c.SealSandboxDeployment([]byte("private-api-key"), "installation", 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range []struct {
		id         string
		generation uint64
	}{{"other", 2}, {"installation", 1}, {"installation", 3}} {
		if _, err := c.OpenSandboxDeployment(encrypted, binding.id, binding.generation); err == nil {
			t.Fatal("accepted different deployment binding")
		}
	}
	plain, err := c.OpenSandboxDeployment(encrypted, "installation", 2)
	if err != nil || string(plain) != "private-api-key" {
		t.Fatal("credential did not roundtrip", err)
	}
}
