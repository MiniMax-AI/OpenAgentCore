//go:build linux

package clirunner

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func waitExited(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		value, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return
		}
		if err == nil {
			fields := strings.Fields(string(value)[strings.LastIndex(string(value), ")")+1:])
			if len(fields) > 0 && fields[0] == "Z" {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("child %d is still running", pid)
}
