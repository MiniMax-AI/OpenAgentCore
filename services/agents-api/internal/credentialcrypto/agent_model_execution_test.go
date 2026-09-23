package credentialcrypto

import (
	"bytes"
	"testing"
)

func TestAgentModelExecutionBinding(t *testing.T) {
	c, err := New(bytes.Repeat([]byte{23}, 32))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("private-provider-canary")
	encrypted, err := c.SealAgentModelExecution(raw, "tenant", "agent")
	if err != nil || bytes.Contains(encrypted, raw) {
		t.Fatal("credential encryption failed")
	}
	opened, err := c.OpenAgentModelExecution(encrypted, "tenant", "agent")
	if err != nil || !bytes.Equal(raw, opened) {
		t.Fatal("credential round trip failed")
	}
	for _, binding := range [][2]string{{"foreign", "agent"}, {"tenant", "other"}, {"", "agent"}, {"tenant", "\xff"}} {
		if _, err := c.OpenAgentModelExecution(encrypted, binding[0], binding[1]); err == nil {
			t.Fatal("incorrect binding accepted")
		}
	}
	if _, err := c.OpenModelExecution(encrypted, "tenant", "agent"); err == nil {
		t.Fatal("Agent ciphertext accepted for Session purpose")
	}
	session, err := c.SealModelExecution(raw, "tenant", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenAgentModelExecution(session, "tenant", "agent"); err == nil {
		t.Fatal("Session ciphertext accepted for Agent purpose")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := c.OpenAgentModelExecution(encrypted, "tenant", "agent"); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}
