package smolvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
	sdk "github.com/smol-machines/smolvm-sdk/smolvm-go"
)

func TestRunCommandCleansStagedInputAfterCanceledUpload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var p *Provider
	var ref sandbox.Reference
	var removed atomic.Bool
	nativeID := uuid.NewString()
	p, ref, _ = testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(sdk.MachineInfo{Name: p.name(ref), Image: p.imageRef, Labels: p.labels(ref, nativeID), State: sdk.MachineStateRunning, CPUs: 2, MemoryMB: 1024})
		case http.MethodPut:
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Errorf("read upload: %v", err)
			}
			cancel() // Upload is uncertain, but Exec has not yet been submitted.
			_, _ = io.WriteString(w, `{}`)
		case http.MethodPost:
			var request sdk.ExecRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode cleanup: %v", err)
			}
			if len(request.Command) != 4 || request.Command[0] != "/bin/rm" || !strings.HasPrefix(request.Command[3], "/home/runtime/.oac/stdin-") {
				t.Errorf("unexpected command after upload: %v", request.Command)
			}
			removed.Store(true)
			_, _ = io.WriteString(w, `{"exitCode":0}`)
		default:
			t.Errorf("unexpected native request: %s", r.Method)
		}
	})
	if err := p.writeReceipt(ref, nativeID); err != nil {
		t.Fatal(err)
	}
	_, err := p.RunCommand(ctx, ref, sandbox.Command{Args: []string{"/bin/cat"}, Stdin: []byte{0xff}})
	if !errors.Is(err, sandbox.ErrCommandUnconfirmed) || !removed.Load() {
		t.Fatalf("canceled upload: %v, cleanup invoked: %t", err, removed.Load())
	}
}
