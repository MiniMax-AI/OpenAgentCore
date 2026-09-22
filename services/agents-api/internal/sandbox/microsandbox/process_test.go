package microsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "microsandbox-test-helper" {
		var q Request
		if json.NewDecoder(os.Stdin).Decode(&q) != nil {
			os.Exit(2)
		}
		switch q.Operation {
		case "delayed":
			time.Sleep(150 * time.Millisecond)
			if os.WriteFile(q.Config.RuntimePath, []byte("settled"), 0600) != nil {
				os.Exit(3)
			}
		case "oversize":
			fmt.Print(strings.Repeat("x", MaxResponseBytes+1))
			os.Exit(0)
		case "trailing":
			fmt.Print("{\"Version\":1}{}")
			os.Exit(0)
		}
		_ = json.NewEncoder(os.Stdout).Encode(Response{Version: ProtocolVersion})
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func processConfig(t *testing.T) Config {
	t.Helper()
	c := testConfig()
	directory := t.TempDir()
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	c.HelperPath = filepath.Join(directory, "microsandbox-test-helper")
	if e = os.Symlink(executable, c.HelperPath); e != nil {
		t.Fatal(e)
	}
	c.RuntimePath = filepath.Join(directory, "settled")
	return c
}
func TestResponseTimeoutDoesNotKillLifecycleOwner(t *testing.T) {
	c := processConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, e := (&ProcessCaller{}).Call(ctx, Request{Operation: "delayed", Config: c})
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("want timeout: %v", e)
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		if data, e := os.ReadFile(c.RuntimePath); e == nil && string(data) == "settled" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("helper was killed before settlement")
}
func TestInvalidOrUnboundedHelperOutputIsUnconfirmed(t *testing.T) {
	for _, mode := range []string{"oversize", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, e := (&ProcessCaller{}).Call(ctx, Request{Operation: mode, Config: processConfig(t)})
			if e == nil {
				t.Fatal("invalid helper output accepted")
			}
		})
	}
}
