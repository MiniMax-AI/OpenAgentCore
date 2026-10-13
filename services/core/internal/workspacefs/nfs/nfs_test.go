package nfs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

func TestCanonicalParameters(t *testing.T) {
	good := `{"root":"/nfs/workspaces","namespace_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","uid":65532}`
	for _, raw := range []string{strings.Replace(good, `"uid":65532`, `"uid":0`, 1), strings.Replace(good, `"uid":65532`, `"uid":null`, 1), strings.Replace(good, `"uid":65532`, `"uid":65532,"uid":65532`, 1), strings.Replace(good, `"root"`, `"Root"`, 1), strings.Replace(good, `"uid":65532`, `"uid":65532,"extra":true`, 1), strings.Replace(good, `/nfs/workspaces`, `/nfs/../workspaces`, 1), good + ` {}`} {
		if _, err := CanonicalParameters(json.RawMessage(raw)); !errors.Is(err, workspacefs.ErrInvalid) {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
	if got, err := CanonicalParameters(json.RawMessage(good)); err != nil || string(got) != good {
		t.Fatalf("canonical: %s %v", got, err)
	}
}
func TestBudgetRetainedAfterTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{}, cap(operations))
	for range cap(operations) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := run(ctx, func() (int, error) { entered <- struct{}{}; <-release; return 9, nil })
			done <- err
		}()
		<-entered
		cancel()
		if err := <-done; !errors.Is(err, workspacefs.ErrUnconfirmed) {
			t.Fatal(err)
		}
	}
	if _, err := run(context.Background(), func() (int, error) { t.Error("exhausted operation executed"); return 0, nil }); !errors.Is(err, workspacefs.ErrUnavailable) {
		t.Fatal(err)
	}
}
func TestLateResultDoesNotEscape(t *testing.T) {
	// The preceding test's kernel stand-ins may be completing asynchronously.
	deadline := time.Now().Add(time.Second)
	for len(operations) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	go func() { <-entered; cancel() }()
	got, err := run(ctx, func() (int, error) { close(entered); <-release; return 42, nil })
	if got != 0 || !errors.Is(err, workspacefs.ErrUnconfirmed) {
		t.Fatalf("%d %v", got, err)
	}
	close(release)
}
