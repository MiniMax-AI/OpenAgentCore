//go:build unix

package claudesdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestCancellationWaitsForDrainAndPublishesOutcome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := cancellationConfig(root, "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A stopped consumer must not prevent native output draining or cancellation.
	out := make(chan proto.Envelope)
	running, err := NewFactory(config)(ctx, cancellationRequest(), out)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Cancel(ctx)
	if event := <-out; event.Type != proto.TypeDelta {
		t.Fatal("missing native readiness barrier")
	}
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	err = running.Cancel(short)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unsettled cancellation reported %v", err)
	}
	if got := running.CancellationOutcome(); !reflect.DeepEqual(got, proto.DonePayload{}) {
		t.Fatal("unsettled outcome was exposed", got)
	}
	if err := running.(*session).Steer(ctx, proto.PromptSteerPayload{InputID: "later", Input: proto.TextInput("later")}); !errors.Is(err, agent.ErrSteeringInactive) {
		t.Fatal("cancelled execution accepted steering", err)
	}
	if err := os.WriteFile(filepath.Join(config.StateDir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := running.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-running.(*session).process.Done():
	default:
		t.Fatal("successful cancellation preceded owned process release")
	}
	got := running.CancellationOutcome()
	if got.Content != "partialtaildrained" || got.Metadata[proto.DoneMetaAgentSessionID] != "native-session" || got.Usage.Raw["claude_sdk_result"] == nil || got.Usage.Tokens != nil {
		t.Fatalf("lost drained cancellation outcome: %+v", got)
	}
	var done proto.DonePayload
	for event := range out {
		if event.Type == proto.TypeDone {
			if err := event.DecodePayload(&done); err != nil {
				t.Fatal(err)
			}
			// Router cleanup calls Cancel while it is still handling Done.
			if err := running.Cancel(ctx); err != nil {
				t.Fatal("completion cleanup waited on terminal delivery", err)
			}
		}
	}
	gotJSON, _ := json.Marshal(got)
	doneJSON, _ := json.Marshal(done)
	if !bytes.Equal(gotJSON, doneJSON) {
		t.Fatal("Done differs from cancellation outcome", done)
	}
}

func TestFailureKeepsOnlyVerifiedNativeIdentity(t *testing.T) {
	for _, mode := range []string{"failure", "wrong-identity", "before-identity"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("OAC_RUNTIME_HOME", root)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := make(chan proto.Envelope, 8)
			running, err := NewFactory(cancellationConfig(root, mode))(ctx, cancellationRequest(), out)
			if err != nil {
				t.Fatal(err)
			}
			defer running.Cancel(ctx)
			failed := false
			var done proto.DonePayload
			for event := range out {
				if event.Type == proto.TypeError {
					failed = true
				}
				if event.Type == proto.TypeDone {
					_ = event.DecodePayload(&done)
				}
			}
			if !failed {
				t.Fatal("native failure was not reported")
			}
			if mode == "failure" {
				if done.Metadata[proto.DoneMetaAgentSessionID] != "native-session" || done.Content != "partial" || done.Usage.Raw["claude_sdk_result"] == nil {
					t.Fatal("verified failure outcome was lost", done)
				}
			} else if done.Metadata[proto.DoneMetaAgentSessionID] != nil {
				t.Fatal("requested or mismatched native identity was exposed", done)
			}
		})
	}
}

func TestCancellationDrainsIntoReadyConsumer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := cancellationConfig(root, "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan proto.Envelope, 16)
	running, err := NewFactory(config)(ctx, cancellationRequest(), out)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Cancel(ctx)
	if event := <-out; event.Type != proto.TypeDelta {
		t.Fatal("missing native readiness barrier")
	}
	if err := os.WriteFile(filepath.Join(config.StateDir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := running.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	var text string
	usage := 0
	for event := range out {
		if event.Type == proto.TypeDelta {
			var delta proto.DeltaPayload
			if err := event.DecodePayload(&delta); err != nil {
				t.Fatal(err)
			}
			text += delta.Delta
		}
		if event.Type == proto.TypeUsage {
			usage++
		}
	}
	if text != "taildrained" || usage != 1 {
		t.Fatalf("ready consumer lost drained observations: text %q, usage %d", text, usage)
	}
}

func cancellationConfig(root, mode string) Config {
	return Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state"), Env: []string{"GO_CLAUDE_SDK_HELPER=1", "SDK_HELPER_MODE=cancellation-" + mode, "GORACE=atexit_sleep_ms=0"}}
}

func cancellationRequest() proto.PromptRequestPayload {
	return proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("hello"), AgentSessionID: "native-session", AgentOptions: map[string]any{"model": "fake-model", "system_prompt": "instructions"}}
}

func runCancellationHelper(request startRequest, mode string, scanner *bufio.Scanner, emit func(bridgeEvent)) {
	if mode == "cancellation-wait" {
		emit(bridgeEvent{Type: "delta", Delta: "partial"})
		if !scanner.Scan() {
			return
		}
		var cancel map[string]any
		if json.Unmarshal(scanner.Bytes(), &cancel) != nil || cancel["type"] != "turn_cancel" {
			os.Exit(9)
		}
		// These valid observations were in flight when cancellation started.
		emit(bridgeEvent{Type: "input_ready", SessionID: request.Resume})
		emit(bridgeEvent{Type: "usage", ResultID: "native-result", SessionID: request.Resume, Usage: json.RawMessage(usageFixture)})
		emit(bridgeEvent{Type: "delta", Delta: "tail"})
		for {
			if _, err := os.Stat(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "release")); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		_, _ = os.Stderr.WriteString(strings.Repeat("x", 2*1024*1024))
		emit(bridgeEvent{Type: "delta", Delta: "drained"})
		emit(bridgeEvent{Type: "input_closed", SessionID: request.Resume})
		emit(bridgeEvent{Type: "error", Code: "cancelled"})
		return
	}
	if mode != "cancellation-before-identity" {
		id := request.Resume
		if mode == "cancellation-wrong-identity" {
			id = "wrong-session"
		}
		emit(bridgeEvent{Type: "input_ready", SessionID: id})
		emit(bridgeEvent{Type: "usage", ResultID: "native-result", SessionID: id, Usage: json.RawMessage(usageFixture)})
		emit(bridgeEvent{Type: "delta", Delta: "partial"})
	}
	emit(bridgeEvent{Type: "error", Code: "execution_failed"})
}
