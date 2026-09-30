package runtimeenrollment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

type EnrollmentStore interface {
	EnrollRuntime(context.Context, string, string) (store.RuntimeEnrollment, error)
}

// EnrollmentHandler is part of our daemon connection contract, not an upstream
// Agents resource. It grants no Session API access and never issues another key.
func EnrollmentHandler(s EnrollmentStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := func(status int) { http.Error(w, http.StatusText(status), status) }
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			fail(http.StatusMethodNotAllowed)
			return
		}
		authorization := strings.Fields(r.Header.Get("Authorization"))
		if len(authorization) != 2 || !strings.EqualFold(authorization[0], "Bearer") {
			fail(http.StatusUnauthorized)
			return
		}
		var input struct {
			EnvironmentID string `json:"environment_id"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if len(r.URL.Query()) != 0 || decoder.Decode(&input) != nil || input.EnvironmentID == "" || decoder.Decode(new(any)) != io.EOF {
			fail(http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		binding, err := s.EnrollRuntime(ctx, input.EnvironmentID, runtimedevice.HashCredential(authorization[1]))
		switch {
		case errors.Is(err, store.ErrNotFound):
			fail(http.StatusUnauthorized)
		case errors.Is(err, store.ErrDeviceBindingConflict):
			fail(http.StatusConflict)
		case err != nil:
			fail(http.StatusServiceUnavailable)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(struct {
				DeviceID           string `json:"device_id"`
				SessionID          string `json:"session_id"`
				EnvironmentID      string `json:"environment_id"`
				WorkspaceDirectory string `json:"workspace_directory"`
			}{binding.DeviceID, binding.SessionID, binding.EnvironmentID, binding.WorkspaceDirectory})
		}
	})
}
