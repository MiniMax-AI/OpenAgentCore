//go:build !windows

package cli

// Unix deletes the running executable with the rest, so nothing is left to schedule.
func deleteAfterExit(string) {}
