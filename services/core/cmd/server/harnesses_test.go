package main

import "testing"

func TestUserManagedHarnessSelectionNeedsNoProvider(t *testing.T) {
	t.Setenv("OAC_HARNESSES", "codex,claude_sdk,mcode")
	kinds, err := enabledHarnesses("codex")
	if err != nil || len(kinds) != 3 {
		t.Fatal(kinds, err)
	}
	t.Setenv("OAC_HARNESSES", "unqualified")
	if _, err = enabledHarnesses("codex"); err == nil {
		t.Fatal("unqualified harness enabled")
	}
}
