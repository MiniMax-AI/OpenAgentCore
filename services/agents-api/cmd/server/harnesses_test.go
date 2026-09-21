package main

import "testing"

func TestUserManagedHarnessSelectionNeedsNoProvider(t *testing.T) {
	t.Setenv("AGENTS_API_HARNESSES", "codex,claude_sdk,mcode")
	kinds, err := enabledHarnesses("codex", nil)
	if err != nil || len(kinds) != 3 {
		t.Fatal(kinds, err)
	}
	t.Setenv("AGENTS_API_HARNESSES", "unqualified")
	if _, err = enabledHarnesses("codex", nil); err == nil {
		t.Fatal("unqualified harness enabled")
	}
}
