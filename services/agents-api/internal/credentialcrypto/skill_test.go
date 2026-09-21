package credentialcrypto

import (
	"bytes"
	"testing"
)

func TestSkillContentBoundToTenantResourceAndVersion(t *testing.T) {
	c := testCipher(t, bytes.Repeat([]byte{29}, 32))
	binding := SkillBinding{TenantID: "tenant", SkillID: "skill", VersionID: "version-id", Version: "1"}
	ciphertext, err := c.SealSkill([]byte("private-bundle"), binding)
	if err != nil {
		t.Fatal(err)
	}
	if body, err := c.OpenSkill(ciphertext, binding); err != nil || string(body) != "private-bundle" {
		t.Fatal("round trip", err)
	}
	for _, change := range []func(*SkillBinding){
		func(b *SkillBinding) { b.TenantID = "other" },
		func(b *SkillBinding) { b.SkillID = "other" },
		func(b *SkillBinding) { b.VersionID = "other" },
		func(b *SkillBinding) { b.Version = "2" },
	} {
		other := binding
		change(&other)
		if _, err := c.OpenSkill(ciphertext, other); err == nil {
			t.Fatal("cross-boundary bundle accepted")
		}
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := c.OpenSkill(ciphertext, binding); err == nil {
		t.Fatal("tampered content accepted")
	}
}
