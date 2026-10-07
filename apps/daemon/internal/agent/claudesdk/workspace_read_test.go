//go:build unix

package claudesdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func runWorkspaceReadHelper(scanner *bufio.Scanner, state, mode string) {
	turnID := ""
	for scanner.Scan() {
		var req struct {
			Type, ID, Path string
			TurnID         string `json:"turn_id"`
			Max            int    `json:"max_bytes"`
		}
		_ = json.Unmarshal(scanner.Bytes(), &req)
		if req.Type == "turn_cancel" {
			reusable := false
			_ = json.NewEncoder(os.Stdout).Encode(bridgeEvent{Type: "error", TurnID: turnID, Code: "cancelled"})
			confirmed := true
			_ = json.NewEncoder(os.Stdout).Encode(bridgeEvent{Type: "turn_settled", Confirmed: &confirmed, TurnID: turnID, Reusable: &reusable, Reason: "fixture_closed"})
			return
		}
		if req.Type == "turn_start" {
			turnID = req.TurnID
			_ = json.NewEncoder(os.Stdout).Encode(bridgeEvent{Type: "turn_started", TurnID: turnID})
			_ = os.WriteFile(filepath.Join(state, "read-started"), []byte("{}"), 0600)
			continue
		}
		_ = os.WriteFile(filepath.Join(state, "read-admitted"), []byte("{}"), 0600)
		if mode == "read-held" {
			for {
				if _, err := os.Stat(filepath.Join(state, "read-release")); err == nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
		}
		if mode == "read-exit" {
			return
		}
		data := bytes.Repeat([]byte{0, 255, 1, 128}, req.Max/4)
		if req.Path == "empty" {
			data = nil
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		truncated := req.Path == "prefix"
		response := map[string]any{"type": "workspace_read", "id": req.ID, "data_base64": encoded, "truncated": truncated}
		switch req.Path {
		case "uncertain":
			response = map[string]any{"type": "workspace_read", "id": req.ID, "error": "uncertain"}
		case "bad-base64":
			response["data_base64"] = "!"
		case "wrong-id":
			response["id"] = "other"
		case "extra":
			response["extra"] = true
		case "oversize":
			response["data_base64"] = base64.StdEncoding.EncodeToString(make([]byte, req.Max+1))
		case "invalid":
			response = map[string]any{"type": "workspace_read", "id": req.ID, "error": "invalid"}
		}
		_ = json.NewEncoder(os.Stdout).Encode(response)
	}
}

func readExecutor(t *testing.T, mode string) (*executor, Config) {
	t.Helper()
	config := preparationFixture(t, mode)
	resource, err := NewExecutorFactory(config)(t.Context(), preparationRequest())
	if err != nil {
		t.Fatal(err)
	}
	e := resource.(*executor)
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	return e, config
}

func TestWorkspaceReadBoundsAndBinary(t *testing.T) {
	e, _ := readExecutor(t, "read-normal")
	for _, path := range []string{"", "/absolute", "../escape", "a//b", "a/./b", "a\\b", "a\x00b", strings.Repeat("界", 3000)} {
		if _, err := e.ReadWorkspaceFile(t.Context(), path, 4); !errors.Is(err, agent.ErrWorkspaceReadInvalid) {
			t.Fatalf("accepted %q: %v", path, err)
		}
	}
	for _, limit := range []int{0, -1, workspaceReadMaxBytes + 1} {
		if _, err := e.ReadWorkspaceFile(t.Context(), "file", limit); err != agent.ErrWorkspaceReadInvalid {
			t.Fatal(limit, err)
		}
	}
	for _, path := range []string{"file", "prefix", "empty"} {
		result, err := e.ReadWorkspaceFile(t.Context(), path, workspaceReadMaxBytes)
		expected := bytes.Repeat([]byte{0, 255, 1, 128}, workspaceReadMaxBytes/4)
		if path == "empty" {
			expected = nil
		}
		if err != nil || !bytes.Equal(result.Data, expected) || result.Truncated != (path == "prefix") {
			t.Fatal(path, err, len(result.Data), result.Truncated)
		}
	}
	if _, err := e.ReadWorkspaceFile(t.Context(), "invalid", 4); err != agent.ErrWorkspaceReadInvalid {
		t.Fatal(err)
	}
	if _, err := e.ReadWorkspaceFile(t.Context(), "file", 4); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceReadDetachAndTurnStart(t *testing.T) {
	e, config := readExecutor(t, "read-held")
	ctx, detach := context.WithCancel(t.Context())
	defer detach()
	result := make(chan error, 1)
	go func() { _, err := e.ReadWorkspaceFile(ctx, "file", 4); result <- err }()
	waitPreparationFile(t, filepath.Join(config.StateDir, "read-admitted"))
	detach()
	if _, err := e.ReadWorkspaceFile(t.Context(), "file", 4); err != agent.ErrWorkspaceReadBusy {
		t.Fatal(err)
	}
	out := make(chan proto.Envelope, 16)
	running, err := e.StartTurn(t.Context(), "run", proto.TextInput("hello"), out)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		t.Fatal("caller detach discarded native wait", err)
	default:
	}
	if err := os.WriteFile(filepath.Join(config.StateDir, "read-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	waitPreparationFile(t, filepath.Join(config.StateDir, "read-started"))
	if _, err := running.(agent.WorkspaceReader).ReadWorkspaceFile(t.Context(), "file", 4); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceReadUnknownAndRelease(t *testing.T) {
	for _, path := range []string{"uncertain", "wrong-id", "bad-base64", "oversize", "extra"} {
		t.Run(path, func(t *testing.T) {
			e, _ := readExecutor(t, "read-normal")
			result, err := e.ReadWorkspaceFile(t.Context(), path, 4)
			if err != agent.ErrWorkspaceReadUncertain || len(result.Data) != 0 {
				t.Fatal(result, err)
			}
			if _, err := e.ReadWorkspaceFile(t.Context(), "file", 4); err != agent.ErrWorkspaceReadUncertain && err != agent.ErrWorkspaceReadUnavailable {
				t.Fatal(err)
			}
		})
	}
	for _, mode := range []string{"read-held", "read-exit"} {
		t.Run(mode, func(t *testing.T) {
			e, config := readExecutor(t, mode)
			done := make(chan error, 1)
			go func() { _, err := e.ReadWorkspaceFile(t.Context(), "file", 4); done <- err }()
			waitPreparationFile(t, filepath.Join(config.StateDir, "read-admitted"))
			if mode == "read-held" {
				if err := e.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err != agent.ErrWorkspaceReadUncertain {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("native waiter lost on release")
			}
		})
	}
}

func TestWorkspaceReadDeadlineStopsOwnerBeforeUnknown(t *testing.T) {
	e, config := readExecutor(t, "read-held")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := e.ReadWorkspaceFile(ctx, "file", 4); done <- err }()
	waitPreparationFile(t, filepath.Join(config.StateDir, "read-admitted"))
	if err := <-done; err != agent.ErrWorkspaceReadUncertain {
		t.Fatal(err)
	}
	if e.base.process.Context().Err() == nil {
		t.Fatal("uncertain deadline returned before owner cancellation")
	}
	if _, err := e.StartTurn(t.Context(), "late", proto.TextInput("hello"), make(chan proto.Envelope, 8)); err == nil {
		t.Fatal("unknown owner accepted a new Turn")
	}
}
