package dispatch

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

type exportSender struct{ replies chan proto.Envelope }

// Send fails with a canceled context, as the connection does.
func (s exportSender) Send(ctx context.Context, env proto.Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.replies <- env:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stubEnvironment is an owner whose export writes outputs/a, 131089 zero
// bytes, and fails after it when fail is set.
type stubEnvironment struct{ fail bool }

func (stubEnvironment) Configure(r proto.PromptRequestPayload) (proto.PromptRequestPayload, error) {
	return r, nil
}
func (stubEnvironment) Prepare(_ context.Context, r proto.PromptRequestPayload) (proto.PromptRequestPayload, error) {
	return r, nil
}
func (stubEnvironment) ApplyRuntimePreparation(context.Context, uuid.UUID, proto.RuntimePreparePayload, []byte) error {
	return errors.New("stub")
}
func (stubEnvironment) ListWorkspaceDirectory(context.Context, string, int) (WorkspaceDirectoryResult, error) {
	return WorkspaceDirectoryResult{}, ErrWorkspaceReadUnavailable
}
func (stubEnvironment) WriteWorkspaceFile(context.Context, string, []byte) (WorkspaceWriteResult, error) {
	return WorkspaceWriteResult{}, ErrWorkspaceWriteUnavailable
}
func (e stubEnvironment) ExportOutputs(_ context.Context, w io.Writer) error {
	archive := tar.NewWriter(w)
	if err := archive.WriteHeader(&tar.Header{Name: "outputs/a", Mode: 0600, Size: 131089, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := archive.Write(make([]byte, 131089)); err != nil {
		return err
	}
	if e.fail {
		return errors.New("output too large")
	}
	return archive.Close()
}
func (stubEnvironment) Close(context.Context) error { return nil }

func exporterRouter(t *testing.T, program string) (*Router, exportSender, proto.WorkspaceExportPayload) {
	t.Helper()
	environment := uuid.NewString()
	owner := stubEnvironment{fail: program == "failure"}
	sender := exportSender{make(chan proto.Envelope, 8)}
	r, err := New(Config{Registry: agent.NewRegistry(), Sender: sender, Environments: func(proto.AssignmentRef, proto.AssignmentBindPayload) Environment { return owner }})
	if err != nil {
		t.Fatal(err)
	}
	bindAssignment(r, capabilityRef, environment)
	handle := uuid.NewString()
	r.preparations[handle] = &preparationState{request: proto.Envelope{Assignment: capabilityRef}, environmentID: environment, owns: true, ctx: context.Background(), deadline: time.Now().Add(time.Hour), status: proto.PreparationStatusPayload{State: "ready"}}
	t.Cleanup(func() {
		r.mu.Lock()
		delete(r.preparations, handle)
		r.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := r.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return r, sender, proto.WorkspaceExportPayload{Step: "begin", Handle: handle, EnvironmentID: environment}
}

func sendExport(t *testing.T, r *Router, id string, p proto.WorkspaceExportPayload) {
	t.Helper()
	env, err := proto.NewEnvelope(proto.TypeWorkspaceExport, id, p)
	if err != nil {
		t.Fatal(err)
	}
	env.Assignment = capabilityRef
	if err := r.Handle(t.Context(), env); err != nil {
		t.Fatal(err)
	}
}
func readExport(t *testing.T, s exportSender) proto.WorkspaceExportResultPayload {
	t.Helper()
	select {
	case env := <-s.replies:
		var p proto.WorkspaceExportResultPayload
		if env.DecodePayload(&p) != nil {
			t.Fatal("invalid reply")
		}
		return p
	case <-time.After(3 * time.Second):
		t.Fatal("missing export reply")
	}
	return proto.WorkspaceExportResultPayload{}
}

func TestWorkspaceExportUsesExactReadPreparationAndPullsBoundedBytes(t *testing.T) {
	r, s, request := exporterRouter(t, "normal")
	for _, field := range []string{"environment", "handle"} {
		bad := request
		if field == "environment" {
			bad.EnvironmentID = uuid.NewString()
		} else {
			bad.Handle = uuid.NewString()
		}
		sendExport(t, r, uuid.NewString(), bad)
		if result := readExport(t, s); result.Outcome != "rejected" {
			t.Fatal("foreign authority accepted")
		}
	}
	id := uuid.NewString()
	sendExport(t, r, id, request)
	var offset int64
	var archive bytes.Buffer
	for {
		p := readExport(t, s)
		if p.Offset != offset {
			t.Fatal("wrong offset")
		}
		if p.Outcome == "completed" {
			break
		}
		if p.Outcome != "chunk" || len(p.Data) == 0 || len(p.Data) > proto.WorkspaceExportChunkBytes {
			t.Fatal("invalid chunk")
		}
		archive.Write(p.Data)
		offset += int64(len(p.Data))
		select {
		case <-s.replies:
			t.Fatal("export pushed unrequested data")
		default:
		}
		sendExport(t, r, id, proto.WorkspaceExportPayload{Step: "next", Offset: offset})
	}
	reader := tar.NewReader(&archive)
	header, err := reader.Next()
	if err != nil || header.Name != "outputs/a" || header.Size != 131089 {
		t.Fatal("invalid archive", header, err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(data, make([]byte, 131089)) {
		t.Fatal("truncated artifact", err)
	}
	if _, err = reader.Next(); err != io.EOF {
		t.Fatal("unexpected extra artifact", err)
	}
}

func TestWorkspaceExportFailureAfterBytesCannotComplete(t *testing.T) {
	r, s, request := exporterRouter(t, "failure")
	id := uuid.NewString()
	sendExport(t, r, id, request)
	var offset int64
	for {
		p := readExport(t, s)
		if p.Outcome == "failed" {
			if offset == 0 {
				t.Fatal("no prefix before failure")
			}
			break
		}
		if p.Outcome != "chunk" || p.Offset != offset {
			t.Fatal("failed export appeared complete", p)
		}
		offset += int64(len(p.Data))
		sendExport(t, r, id, proto.WorkspaceExportPayload{Step: "next", Offset: offset})
	}
}

func TestWorkspaceExportCancelUnblocksWriterAndReleasesCapacity(t *testing.T) {
	r, _, request := exporterRouter(t, "normal")
	id := uuid.NewString()
	sendExport(t, r, id, request)
	sendExport(t, r, id, proto.WorkspaceExportPayload{Step: "cancel"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		active := r.workspaceExport != nil
		r.mu.Unlock()
		if !active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("export cancellation did not release capacity")
}

func TestCanceledExportAnswersTheRequestCoreAwaits(t *testing.T) {
	r, s, request := exporterRouter(t, "normal")
	preparation, release := context.WithCancel(context.Background())
	r.mu.Lock()
	r.preparations[request.Handle].ctx = preparation
	r.mu.Unlock()
	id := uuid.NewString()
	sendExport(t, r, id, request)
	first := readExport(t, s)
	if first.Outcome != "chunk" {
		t.Fatal(first)
	}
	// Core asks for the next chunk as the preparation that owns the export is released.
	r.mu.Lock()
	release()
	r.workspaceExport.requests <- proto.WorkspaceExportPayload{Step: "next", Offset: int64(len(first.Data))}
	r.mu.Unlock()
	if got := readExport(t, s); got.Outcome != "failed" && got.Outcome != "chunk" {
		t.Fatal("the canceled export answered with", got)
	}
}
