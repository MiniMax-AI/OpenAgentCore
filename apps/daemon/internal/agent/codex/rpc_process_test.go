package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestJSONRPCClientCloseReapsChildProcess(t *testing.T) {
	cfg := JSONRPCConfig{
		Binary:         os.Args[0],
		ExtraArgs:      []string{"-test.run=TestJSONRPCClientFakeCodexProcess", "--"},
		Env:            append(os.Environ(), "CODEX_RPC_FAKE_PROCESS=1"),
		LogTag:         "codex-rpc-process-test",
		RequestTimeout: 2 * time.Second,
	}
	client := NewJSONRPCClient(cfg)

	_, err := client.Start(context.Background(), InitializeParams{
		ClientInfo: InitializeClientInfo{Name: "test", Version: "0"},
	})
	if err != nil {
		t.Fatalf("start fake process: %v", err)
	}
	if client.process == nil || client.process.Cmd.Process == nil {
		t.Fatal("client did not spawn a child process")
	}

	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-client.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("child process was not reaped")
	}
	if _, exited := client.process.ExitCode(); !exited {
		t.Fatal("process exit not recorded after close")
	}
}

func TestJSONRPCClientResponseTimeoutStartsAfterWrite(t *testing.T) {
	client, server, cleanup := NewTestClient()
	defer cleanup()
	client.cfg.RequestTimeout = 100 * time.Millisecond
	responseDone := make(chan error, 1)
	go func() {
		// Block the pipe write for longer than the response timeout.
		time.Sleep(200 * time.Millisecond)
		var request JsonRpcRequest
		if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
			responseDone <- err
			return
		}
		time.Sleep(10 * time.Millisecond)
		responseDone <- json.NewEncoder(server.ToClient).Encode(JsonRpcResponse{
			JsonRpc: JsonRpcVersion, ID: request.ID, Result: "ok",
		})
	}()
	result, err := client.Request(context.Background(), "echo", nil)
	if responseErr := <-responseDone; responseErr != nil {
		t.Fatalf("send response: %v", responseErr)
	}
	if err != nil || string(result) != `"ok"` {
		t.Fatalf("response after blocked write: result=%s error=%v", result, err)
	}
}

func TestJSONRPCClientInitializeUsesDefaultRequestBudget(t *testing.T) {
	client := NewJSONRPCClient(JSONRPCConfig{
		Binary: os.Args[0], ExtraArgs: []string{"-test.run=^TestJSONRPCClientFakeCodexProcess$", "--"},
		Env: append(os.Environ(), "CODEX_RPC_FAKE_PROCESS=1", "CODEX_RPC_FAKE_INITIALIZE_DELAY=11s"),
	})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	result, err := client.Start(ctx, InitializeParams{})
	if err != nil || result.UserAgent != "fake-codex" {
		t.Fatalf("initialize after remote metadata work: result=%+v error=%v", result, err)
	}
}

func TestJSONRPCClientInitializeFailureReapsChild(t *testing.T) {
	for _, mode := range []string{"request-timeout", "owner-cancel", "blocked-write-cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cfg := JSONRPCConfig{
				Binary: os.Args[0], ExtraArgs: []string{"-test.run=^TestJSONRPCClientFakeCodexProcess$", "--"},
				Env: append(os.Environ(), "CODEX_RPC_FAKE_PROCESS=1", "CODEX_RPC_FAKE_INITIALIZE_DELAY=1m"),
			}
			if mode == "request-timeout" {
				cfg.RequestTimeout = 100 * time.Millisecond
			}
			init := InitializeParams{}
			if mode == "blocked-write-cancel" {
				cfg.Env = append(cfg.Env, "CODEX_RPC_FAKE_BLOCK_INITIALIZE=1")
				init.ClientInfo.Name = strings.Repeat("x", 4*1024*1024)
			}
			client := NewJSONRPCClient(cfg)
			t.Cleanup(func() { _ = client.Close() })
			if mode != "request-timeout" {
				client.OnNotification("test/initialize_waiting", func(json.RawMessage) { cancel() })
			}
			_, err := client.Start(ctx, init)
			if err == nil {
				t.Fatal("initialization unexpectedly succeeded")
			}
			if mode == "request-timeout" && !strings.Contains(err.Error(), "initialize timed out after 100ms") {
				t.Fatalf("configured request budget did not end initialization: %v", err)
			}
			if mode != "request-timeout" && !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("initialization did not observe the explicit owner cancellation", ctx.Err())
			}
			select {
			case <-client.Done():
			default:
				t.Fatal("failed initialization returned before reaping its child")
			}
			if _, exited := client.process.ExitCode(); !exited || client.Alive() {
				t.Fatal("failed initialization retained a live child")
			}
		})
	}
}

func TestJSONRPCClientFakeCodexProcess(t *testing.T) {
	if os.Getenv("CODEX_RPC_FAKE_PROCESS") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	if os.Getenv("CODEX_RPC_FAKE_BLOCK_INITIALIZE") == "1" {
		if _, err := reader.ReadByte(); err != nil {
			os.Exit(4)
		}
		fmt.Println(`{"jsonrpc":"2.0","method":"test/initialize_waiting"}`)
		for {
			time.Sleep(time.Second)
		}
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "read initialize: %v\n", err)
		os.Exit(2)
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		fmt.Fprintf(os.Stderr, "decode initialize: %v\n", err)
		os.Exit(2)
	}
	if delay := os.Getenv("CODEX_RPC_FAKE_INITIALIZE_DELAY"); delay != "" {
		duration, err := time.ParseDuration(delay)
		if err != nil {
			os.Exit(5)
		}
		fmt.Println(`{"jsonrpc":"2.0","method":"test/initialize_waiting"}`)
		time.Sleep(duration)
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result": map[string]any{
			"userAgent": "fake-codex",
			"codexHome": "/tmp/fake-codex-home",
		},
	}
	body, _ := json.Marshal(resp)
	fmt.Printf("%s\n", body)
	if os.Getenv("CODEX_RPC_FAKE_BLOCK_WRITE") == "1" {
		// Initialization already completed; consume only a prefix of the next frame.
		if _, err := reader.ReadByte(); err != nil {
			os.Exit(4)
		}
		fmt.Println(`{"jsonrpc":"2.0","method":"test/write_blocked"}`)
	}
	for {
		time.Sleep(time.Second)
	}
}
