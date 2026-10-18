package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func helperOptions(t *testing.T, mode string, timeout time.Duration) StartOptions {
	t.Helper()
	return StartOptions{
		Binary:         os.Args[0],
		Args:           []string{"-test.run=^TestRPCProcessHelper$", "--", mode},
		Dir:            t.TempDir(),
		Env:            append(os.Environ(), "GO_WANT_PI_RPC_HELPER=1"),
		CommandTimeout: timeout,
		KillTimeout:    time.Second,
	}
}

func TestCallReturnsResponseData(t *testing.T) {
	process, err := startRPC(context.Background(), helperOptions(t, "ok", 5*time.Second))
	if err != nil {
		t.Fatalf("startRPC: %v", err)
	}
	defer process.Close()

	data, err := process.call(context.Background(), "ping", map[string]any{"value": "x"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var decoded struct {
		Command string `json:"command"`
		Value   string `json:"value"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("data: %v", err)
	}
	if decoded.Command != "ping" || decoded.Value != "x" {
		t.Fatalf("unexpected data: %+v", decoded)
	}
	select {
	case event := <-process.Events():
		if event.Type != "agent_start" {
			t.Fatalf("unexpected event type %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestCallReportsAdapterError(t *testing.T) {
	process, err := startRPC(context.Background(), helperOptions(t, "fail", 5*time.Second))
	if err != nil {
		t.Fatalf("startRPC: %v", err)
	}
	defer process.Close()

	_, err = process.call(context.Background(), "ping", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown command: ping") {
		t.Fatalf("expected adapter error, got %v", err)
	}
}

func TestCallTimesOut(t *testing.T) {
	process, err := startRPC(context.Background(), helperOptions(t, "stall", 50*time.Millisecond))
	if err != nil {
		t.Fatalf("startRPC: %v", err)
	}
	defer process.Close()

	_, err = process.call(context.Background(), "ping", nil)
	if !errors.Is(err, errRPCTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestCallFailsWhenProcessExits(t *testing.T) {
	// The child shuts down on its own schedule; the generous command timeout
	// proves the failure comes from the exit, not from the deadline.
	process, err := startRPC(context.Background(), helperOptions(t, "exit", 10*time.Second))
	if err != nil {
		t.Fatalf("startRPC: %v", err)
	}
	defer process.Close()

	if _, err := process.call(context.Background(), "ping", nil); !errors.Is(err, errRPCClosed) {
		t.Fatalf("expected closed error, got %v", err)
	}
}

func TestNonJSONRecordFailsTheStream(t *testing.T) {
	process, err := startRPC(context.Background(), helperOptions(t, "badjson", time.Second))
	if err != nil {
		t.Fatalf("startRPC: %v", err)
	}
	defer process.Close()

	select {
	case _, open := <-process.Events():
		if open {
			t.Fatal("expected closed event stream")
		}
	case <-time.After(time.Second):
		t.Fatal("event stream did not close")
	}
	if _, err := process.call(context.Background(), "ping", nil); !errors.Is(err, errRPCClosed) {
		t.Fatalf("expected closed error, got %v", err)
	}
}

func TestCloseStopsLiveProcess(t *testing.T) {
	process, err := startRPC(context.Background(), helperOptions(t, "stall", 100*time.Millisecond))
	if err != nil {
		t.Fatalf("startRPC: %v", err)
	}
	if _, err := process.call(context.Background(), "ping", nil); !errors.Is(err, errRPCTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestRPCProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PI_RPC_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	switch args[1] {
	case "exit":
		os.Exit(0)
	case "badjson":
		os.Stdout.WriteString("not json\n")
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		switch args[1] {
		case "stall":
			continue
		case "fail":
			if err := writeRecord(map[string]any{"id": request.ID, "type": "response", "command": request.Type, "success": false, "error": "unknown command: " + request.Type}); err != nil {
				os.Exit(2)
			}
		default:
			if err := writeRecord(map[string]any{"type": "agent_start"}); err != nil {
				os.Exit(2)
			}
			if err := writeRecord(map[string]any{"id": request.ID, "type": "response", "command": request.Type, "success": true, "data": map[string]any{"command": request.Type, "value": "x"}}); err != nil {
				os.Exit(2)
			}
		}
	}
	os.Exit(0)
}

func writeRecord(value map[string]any) error {
	line, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(line, '\n'))
	return err
}
