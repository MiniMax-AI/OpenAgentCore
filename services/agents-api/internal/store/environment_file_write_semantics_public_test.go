package store_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type fileCreateResponse struct {
	status int
	body   string
}

// serveFileWrite answers one complete daemon transfer with the final result.
func serveFileWrite(h *dispatchHarness, final proto.WorkspaceWriteResultPayload) string {
	h.t.Helper()
	begin := h.read(proto.TypeWorkspaceWrite)
	var request proto.WorkspaceWritePayload
	if begin.DecodePayload(&request) != nil || request.Step != "begin" {
		h.t.Fatal("write did not begin")
	}
	h.write(begin.ID, proto.TypeWorkspaceWriteResult, proto.WorkspaceWriteResultPayload{Outcome: "ready"})
	for offset := 0; offset < request.SizeBytes; {
		chunk := h.read(proto.TypeWorkspaceWrite)
		var body proto.WorkspaceWritePayload
		if chunk.ID != begin.ID || chunk.DecodePayload(&body) != nil || body.Step != "chunk" {
			h.t.Fatal("chunk changed")
		}
		offset += len(body.Data)
		h.write(begin.ID, proto.TypeWorkspaceWriteResult, proto.WorkspaceWriteResultPayload{Outcome: "received", Offset: offset})
	}
	if commit := h.read(proto.TypeWorkspaceWrite); commit.ID != begin.ID {
		h.t.Fatal("commit changed")
	}
	h.write(begin.ID, proto.TypeWorkspaceWriteResult, final)
	return begin.ID
}

func TestEnvironmentFileCreateRejectionsLeaveNoReceiptOrConsumption(t *testing.T) {
	h, w, environment := localWorker(t, true, false)
	_, pool := store.NewTestStore(t)
	if _, err := pool.Exec(t.Context(), `UPDATE environments SET status='connected' WHERE id=$1`, environment.ID); err != nil {
		t.Fatal(err)
	}
	token, other := uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(token), TenantID: h.tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "tenant-b", TokenSHA256: device.HashCredential(other), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(h.s, auth, "codex", api.WithEnvironmentFileWriter(w), api.WithSourceFiles(h.s))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	source, err := h.s.CreateSourceFile(t.Context(), h.tenant, func(out io.Writer) (store.SourceFileUpload, error) {
		_, err := out.Write([]byte("src"))
		return store.SourceFileUpload{Filename: "source.txt", Purpose: "user_data"}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	post := func(key, id, body string) <-chan fileCreateResponse {
		done := make(chan fileCreateResponse, 1)
		go func() {
			r, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/agents/environments/"+id+"/files", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("OpenAI-Beta", "agents=v1")
			r.Header.Set("Content-Type", "application/json")
			resp, err := server.Client().Do(r)
			if err != nil {
				done <- fileCreateResponse{}
				return
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			done <- fileCreateResponse{resp.StatusCode, string(data)}
		}()
		return done
	}
	await := func(done <-chan fileCreateResponse) fileCreateResponse {
		select {
		case got := <-done:
			return got
		case <-time.After(10 * time.Second):
			t.Fatal("create did not return")
			return fileCreateResponse{}
		}
	}
	states := func() map[string]int {
		rows, err := pool.Query(t.Context(), `SELECT state, count(*) FROM environment_file_writes WHERE environment_id=$1 GROUP BY state`, environment.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := map[string]int{}
		for rows.Next() {
			var state string
			var count int
			if err := rows.Scan(&state, &count); err != nil {
				t.Fatal(err)
			}
			result[state] = count
		}
		return result
	}
	assertError := func(got fileCreateResponse, message string) {
		t.Helper()
		var body map[string]any
		want := map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": "invalid_request_error", "param": nil, "message": message}}
		if got.status != 400 || json.Unmarshal([]byte(got.body), &body) != nil || !reflect.DeepEqual(body, want) {
			t.Fatalf("status=%d body=%s want=%v", got.status, got.body, want)
		}
	}
	inline := `{"type":"inline","data":"YWJj","path":"/workspace/n1/existing.txt"}`
	copyBody := `{"type":"file_id","file_id":"` + source.ID + `","path":"/workspace/link/copy.txt"}`
	rejected := func(reason string) proto.WorkspaceWriteResultPayload {
		return proto.WorkspaceWriteResultPayload{Outcome: "rejected", ErrorCode: "write_rejected", Reason: reason}
	}
	for _, tc := range []struct {
		body, reason, message string
	}{
		{`{"type":"inline","data":"YWJj","path":"/workspace/n1"}`, proto.WorkspaceWriteReasonDirectory, "file path conflicts with an existing environment file"},
		{inline, proto.WorkspaceWriteReasonUnsafe, "environment.files paths must not traverse symlinks or overwrite existing files"},
		{copyBody, proto.WorkspaceWriteReasonUnsafe, "environment.files paths must not traverse symlinks or overwrite existing files"},
	} {
		done := post(token, environment.ID, tc.body)
		id := serveFileWrite(h, rejected(tc.reason))
		assertError(await(done), tc.message)
		if intent, err := h.s.GetEnvironmentFileWrite(t.Context(), h.tenant, environment.ID, id); err != nil || intent.State != "rejected" {
			t.Fatal("rejection did not settle", intent, err)
		}
	}
	// The rejected copy did not consume its Source File.
	if got, err := h.s.GetSourceFile(t.Context(), h.tenant, source.ID); err != nil || got.SizeBytes != 3 {
		t.Fatal("source file consumed", got, err)
	}
	// The inline bound is checked before any mutation intent or dispatch.
	oversized, _ := json.Marshal(map[string]string{"type": "inline", "path": "/workspace/big.bin", "data": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 5<<20+1))})
	assertError(await(post(token, environment.ID, string(oversized))), "environment.files[0].data exceeds the 5 MiB decoded limit")
	// Tenant B sees the Environment as missing, before body inspection.
	foreign, missing := await(post(other, environment.ID, inline)), await(post(token, uuid.NewString(), inline))
	if foreign.status != 404 || foreign != missing {
		t.Fatal("tenant B reached the Environment", foreign, missing)
	}
	if got := states(); !reflect.DeepEqual(got, map[string]int{"rejected": 3}) {
		t.Fatal("rejections left a receipt or blocking intent", got)
	}
	// Known rejections release the mutation owner for a successor.
	done := post(token, environment.ID, copyBody)
	serveFileWrite(h, proto.WorkspaceWriteResultPayload{Outcome: "completed", SizeBytes: 3})
	if got := await(done); got.status != 201 {
		t.Fatal("successor rejected", got)
	}
	if got := states(); !reflect.DeepEqual(got, map[string]int{"rejected": 3, "committed": 1}) {
		t.Fatal("successor receipt", got)
	}
	session, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil {
		t.Fatal("file writes created model execution", err)
	}
}
