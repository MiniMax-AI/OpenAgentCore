package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// Files.create write semantics rows FW2–FW6 of the workspace-file-writes batch.

func inlineCreateBody(size int) string {
	body, _ := json.Marshal(map[string]any{"type": "inline", "path": "/workspace/n1/n2/big.bin", "data": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, size))})
	return string(body)
}

func TestEnvironmentFileCreateInlineDecodedLimit(t *testing.T) {
	const message = "environment.files[0].data exceeds the 5 MiB decoded limit"
	h, f := environmentFileCreateHandler(t)
	if w := requestCreateEnvironmentFile(h, f.environment.ID, inlineCreateBody(5<<20), "files-key"); w.Code != 201 || f.writes != 1 || len(f.data) != 5<<20 || f.path != "n1/n2/big.bin" {
		t.Fatal("exact 5 MiB inline rejected", w.Code, w.Body)
	}
	for _, size := range []int{5<<20 + 1, 50 << 20} {
		assertListQueryError(t, requestCreateEnvironmentFile(h, f.environment.ID, inlineCreateBody(size), "files-key"), "invalid_request_error", nil, message)
	}
	// The bound is checked before provisioning or any Runtime work.
	f.environment.Configuration, f.environment.Status = json.RawMessage(`{"type":"openai_hosted","network":{"access":"disabled"}}`), "pending"
	assertListQueryError(t, requestCreateEnvironmentFile(h, f.environment.ID, inlineCreateBody(5<<20+1), "files-key"), "invalid_request_error", nil, message)
	if f.writes != 1 {
		t.Fatal("oversized inline data reached the writer", f.writes)
	}
	// Tenant isolation precedes the body.
	foreign := requestCreateEnvironmentFile(h, f.environment.ID, inlineCreateBody(5<<20+1), "other-key")
	missing := requestCreateEnvironmentFile(h, uuid.NewString(), inlineCreateBody(5<<20+1), "files-key")
	if foreign.Code != 404 || foreign.Body.String() != missing.Body.String() {
		t.Fatal("foreign oversized create inspected", foreign.Code, foreign.Body)
	}
}

func TestEnvironmentFileCreateSourceCopyKeepsDestinationBound(t *testing.T) {
	sources := &sourceFilesFixture{}
	h, f := environmentFileCreateHandler(t, WithSourceFiles(sources))
	data := bytes.Repeat([]byte{9}, 6<<20)
	sources.tenant, sources.data = f.environment.TenantID, data
	sources.file = store.SourceFile{ID: "file-" + uuid.NewString(), SizeBytes: int64(len(data)), CreatedAt: time.Unix(1, 0)}
	body := `{"type":"file_id","file_id":"` + sources.file.ID + `","path":"/workspace/copy.bin"}`
	if w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key"); w.Code != 201 || f.writes != 1 || !bytes.Equal(f.data, data) {
		t.Fatal("6 MiB source copy rejected", w.Code, w.Body)
	}
}

func TestEnvironmentFileCreateDestinationConflicts(t *testing.T) {
	const conflict = "file path conflicts with an existing environment file"
	const unsafe = "environment.files paths must not traverse symlinks or overwrite existing files"
	h, f := environmentFileCreateHandler(t)
	body := `{"type":"inline","data":"YWJj","path":"/workspace/n1"}`
	for _, tc := range []struct {
		err     error
		code    any
		message string
	}{
		{execution.ErrEnvironmentFileDirectory, "invalid_request_error", conflict},
		{execution.ErrEnvironmentFileUnsafe, "invalid_request_error", unsafe},
		// A generic installer rejection keeps the local code.
		{store.ErrInvalidInput, "invalid_request", "Invalid resource identifier or request limits."},
	} {
		f.err = tc.err
		assertListQueryError(t, requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key"), tc.code, nil, tc.message)
	}
	f.err = execution.ErrExecutionUnavailable
	if w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key"); w.Code != 503 {
		t.Fatal("unknown outcome changed", w.Code, w.Body)
	}
}
