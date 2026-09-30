package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// The /v1 Skills routes keep their observed error fields; the /core/v1 Project
// Skills routes use the Beta ones.
func TestWriteSkillsError(t *testing.T) {
	const project = "/core/v1/projects/project/skills/skill_missing"
	for _, test := range []struct {
		name, path string
		err        error
		status     int
		body       string
	}{
		{"default version", "/v1/skills/skill_missing/versions/1", skills.ErrDefaultVersion, http.StatusBadRequest,
			`{"error":{"message":"Cannot delete the default skill version.","type":"invalid_request_error","code":"invalid_value","param":"version"}}`},
		{"cursor", "/v1/skills/skill_missing/versions", &skills.CursorError{Message: "Skill version cursor does not match this skill."}, http.StatusBadRequest,
			`{"error":{"message":"Skill version cursor does not match this skill.","type":"invalid_request_error","code":"invalid_value","param":"after"}}`},
		{"project cursor", project + "/versions", &skills.CursorError{Message: "Skill version cursor does not match this skill."}, http.StatusBadRequest,
			`{"error":{"message":"Skill version cursor does not match this skill.","type":"invalid_request_error","code":"invalid_request_error","param":null}}`},
		{"list not found", "/v1/skills", skills.ErrNotFound, http.StatusNotFound,
			`{"error":{"message":"Resource not found.","type":"invalid_request_error","code":null,"param":null}}`},
		{"version not found", "/v1/skills/skill_missing/versions/1", fmt.Errorf("lookup: %w", skills.ErrNotFound), http.StatusNotFound,
			`{"error":{"message":"Resource not found.","type":"invalid_request_error","code":null,"param":null}}`},
		{"project not found", project, skills.ErrNotFound, http.StatusNotFound,
			`{"error":{"message":"Resource not found.","type":"not_found_error","code":"not_found_error","param":null}}`},
		{"invalid input", "/v1/skills", skills.ErrInvalidInput, http.StatusBadRequest,
			`{"error":{"message":"Invalid resource identifier or request limits.","type":"invalid_request_error","code":"invalid_request","param":null}}`},
		{"unstorable text", "/v1/skills", fmt.Errorf("create: %w", textvalue.ErrUnstorable), http.StatusBadRequest,
			`{"error":{"message":"` + unstorableTextMessage + `","type":"invalid_request_error","code":"invalid_request_error","param":null}}`},
		{"audit source", "/v1/skills", fmt.Errorf("record: %w", writeaudit.ErrInvalidSource), http.StatusBadRequest,
			`{"error":{"message":"` + invalidInputMessage + `","type":"invalid_request_error","code":"invalid_request","param":null}}`},
		{"credential key missing", "/v1/skills", credentialcrypto.ErrUnavailable, http.StatusServiceUnavailable,
			`{"error":{"message":"Credential encryption is not configured on this service.","type":"server_error","code":"credential_storage_unavailable","param":null}}`},
		{"unknown", "/v1/skills", errors.New("connection reset"), http.StatusInternalServerError,
			`{"error":{"message":"The operation could not be completed.","type":"server_error","code":"internal_error","param":null}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeSkillsError(response, httptest.NewRequest(http.MethodGet, test.path, nil), test.err)
			if response.Code != test.status || response.Body.String() != test.body+"\n" {
				t.Fatalf("%d %s", response.Code, response.Body)
			}
		})
	}
}
