package codex

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
)

func TestJSONRPCClientCloseCanRetryUnreapedChild(t *testing.T) {
	process, err := clirunner.Start(clirunner.StartOptions{Parent: t.Context(), Binary: os.Args[0], Args: []string{"-test.run=^TestJSONRPCClientFakeCodexProcess$", "--"}, Env: append(os.Environ(), "CODEX_RPC_FAKE_PROCESS=1", "GORACE=atexit_sleep_ms=0"), NeedStdin: true, OwnProcessGroup: true})
	if err != nil {
		t.Fatal(err)
	}
	cmd, stdin, stdout := process.Cmd, process.Stdin, process.Stdout
	client := NewJSONRPCClient(JSONRPCConfig{})
	input := &countedCloseWriter{WriteCloser: stdin}
	client.process, client.cmd, client.stdin, client.stdout, client.alive = process, cmd, input, stdout, true
	var reap sync.Once
	t.Cleanup(func() {
		process.Cancel()
		reap.Do(client.waitChild)
	})
	if _, err := io.WriteString(stdin, "{\"id\":\"close-test\"}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(stdout).ReadBytes('\n'); err != nil {
		t.Fatal(err)
	}
	pending := &pendingRequest{resp: make(chan rpcResponse, 1)}
	client.pending["pending"] = pending

	// Keep Wait under test control so a killed child remains unacknowledged.
	if err := client.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close before reaping: got %v, want deadline exceeded", err)
	}
	if client.Alive() {
		t.Fatal("closed client still admits requests")
	}
	select {
	case response := <-pending.resp:
		if response.err == nil {
			t.Fatal("pending request succeeded after close")
		}
	default:
		t.Fatal("close left a pending request")
	}
	select {
	case <-client.Done():
		t.Fatal("Done closed before reaping")
	default:
	}

	closeConcurrently := func(wantTimeout bool) {
		t.Helper()
		results := make(chan error, 4)
		for range cap(results) {
			go func() { results <- client.Close() }()
		}
		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		for range cap(results) {
			select {
			case err := <-results:
				if wantTimeout && !errors.Is(err, context.DeadlineExceeded) || !wantTimeout && err != nil {
					t.Fatalf("concurrent Close: timeout=%t, error=%v", wantTimeout, err)
				}
			case <-deadline.C:
				t.Fatal("concurrent Close did not finish")
			}
		}
	}
	closeConcurrently(true)
	ownerCtx, cancelOwner := context.WithCancel(t.Context())
	s := &Session{rpc: client, cancelCtx: ownerCtx, cancelFn: cancelOwner,
		cfg: defaultSessionConfig(), interactions: newPendingCodexInteractions(), bufs: NewItemBuffers()}
	ctx, cancelWait := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancelWait()
	if err := s.Cancel(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Session cancellation before reap: %v", err)
	}
	reap.Do(client.waitChild)
	if err := s.Cancel(t.Context()); err != nil {
		t.Fatalf("Session cancellation retry after reap: %v", err)
	}
	closeConcurrently(false)
	if client.cmd != cmd || cmd.ProcessState == nil {
		t.Fatal("Close did not retain and reap its original child")
	}
	if got := input.closes.Load(); got != 1 {
		t.Fatalf("stdin closed %d times, want one shutdown initiation", got)
	}
}

type countedCloseWriter struct {
	io.WriteCloser
	closes atomic.Int32
}

func (w *countedCloseWriter) Close() error {
	w.closes.Add(1)
	return w.WriteCloser.Close()
}
