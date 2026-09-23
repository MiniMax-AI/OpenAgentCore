package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type EnvironmentExecutorStore interface {
	IssueEnvironmentExecutorCredential(context.Context, identity.Principal, string, string, bool) (store.IssuedExecutorCredential, error)
	RevokeEnvironmentExecutorCredential(context.Context, identity.Principal, string, string) error
}

type EnvironmentExecutorCredentialRequest struct {
	KeyID  string `json:"key_id"`
	Rotate bool   `json:"rotate,omitempty"`
}

func (h *Handler) registerEnvironmentExecutorRoutes(r chi.Router) {
	s, ok := h.store.(EnvironmentExecutorStore)
	if !ok {
		return
	}
	r.Route("/core/v1/environments/{environment_id}/executor-credentials", func(r chi.Router) {
		r.Use(h.authenticateProject)
		r.Post("/", func(w http.ResponseWriter, r *http.Request) { h.issueEnvironmentExecutorCredential(w, r, s) })
		r.Delete("/{key_id}", func(w http.ResponseWriter, r *http.Request) { h.revokeEnvironmentExecutorCredential(w, r, s) })
	})
}

// @Summary Issue or explicitly rotate an Environment executor credential
// @Description Core extension, not an upstream Agents API operation. Uses the project caller key and exact live self_hosted Session creator. Returns a connect-only secret once, restricted to this Environment. Repeating an issuance key_id returns 409; after an uncertain response explicitly rotate that same key_id. Rotation retains immutable ownership and invalidates the old secret. No Session API authority is granted.
// @Tags Environment Executor
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param environment_id path string true "Environment UUID"
// @Param body body api.EnvironmentExecutorCredentialRequest true "Request"
// @Success 201 {object} store.IssuedExecutorCredential
// @Failure 400,401,404,409,500 {object} v1.ErrorResponse
// @Router /core/v1/environments/{environment_id}/executor-credentials [post]
func (h *Handler) issueEnvironmentExecutorCredential(w http.ResponseWriter, r *http.Request, s EnvironmentExecutorStore) {
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var input EnvironmentExecutorCredentialRequest
	var fields map[string]json.RawMessage
	if decodeInputObject(raw, &input, "key_id", "rotate") != nil || json.Unmarshal(raw, &fields) != nil || bytes.Equal(bytes.TrimSpace(fields["rotate"]), []byte("null")) || !executorManagementID(input.KeyID) {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	credential, err := s.IssueEnvironmentExecutorCredential(r.Context(), r.Context().Value(principalContextKey{}).(identity.Principal), chi.URLParam(r, "environment_id"), input.KeyID, input.Rotate)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, credential)
}

// @Summary Revoke an Environment executor credential
// @Description Core extension authenticated by the project caller key. Revokes only the caller's exact Environment-restricted credential. Repeated revocation is safe. Revocation denies future enrollment and dispatch but does not stop caller-owned compute or prove process quiescence.
// @Tags Environment Executor
// @Security BearerAuth
// @Param environment_id path string true "Environment UUID"
// @Param key_id path string true "Executor key UUID"
// @Success 204
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/environments/{environment_id}/executor-credentials/{key_id} [delete]
func (h *Handler) revokeEnvironmentExecutorCredential(w http.ResponseWriter, r *http.Request, s EnvironmentExecutorStore) {
	keyID := chi.URLParam(r, "key_id")
	if !executorManagementID(keyID) {
		writeStoreError(w, r, store.ErrNotFound)
		return
	}
	if err := s.RevokeEnvironmentExecutorCredential(r.Context(), r.Context().Value(principalContextKey{}).(identity.Principal), chi.URLParam(r, "environment_id"), keyID); err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func executorManagementID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
