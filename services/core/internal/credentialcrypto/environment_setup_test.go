package credentialcrypto

import (
	"bytes"
	"testing"
)

func TestEnvironmentSetupResourceAndFieldBinding(t *testing.T) {
	c := testCipher(t, bytes.Repeat([]byte{3}, 32))
	binding := EnvironmentSetupBinding{TenantID: "tenant", Resource: "environment_template", OwnerID: "owner", Field: "env"}
	plaintext := []byte("private-configuration")
	encrypted, err := c.SealEnvironmentSetup(plaintext, binding)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.OpenEnvironmentSetup(encrypted, binding)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatal("round trip", err)
	}
	for _, change := range []func(*EnvironmentSetupBinding){func(b *EnvironmentSetupBinding) { b.TenantID = "other" }, func(b *EnvironmentSetupBinding) { b.Resource = "environment" }, func(b *EnvironmentSetupBinding) { b.OwnerID = "other" }, func(b *EnvironmentSetupBinding) { b.Field = "setup_commands" }} {
		other := binding
		change(&other)
		if _, err := c.OpenEnvironmentSetup(encrypted, other); err == nil {
			t.Fatal("cross-boundary decryption")
		}
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := c.OpenEnvironmentSetup(encrypted, binding); err == nil {
		t.Fatal("modified ciphertext accepted")
	}
}

func TestAutomationTargetBinding(t *testing.T) {
	c := testCipher(t, bytes.Repeat([]byte{3}, 32))
	encrypted, err := c.SealAutomationTarget([]byte("private"), "tenant", "automation")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.OpenAutomationTarget(encrypted, "tenant", "automation"); err != nil || string(got) != "private" {
		t.Fatal("round trip", err)
	}
	for _, other := range [][2]string{{"other", "automation"}, {"tenant", "other"}} {
		if _, err := c.OpenAutomationTarget(encrypted, other[0], other[1]); err == nil {
			t.Fatal("cross-boundary decryption")
		}
	}
}
