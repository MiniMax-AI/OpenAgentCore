package runtimeenrollment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type EnrollmentStore interface {
	EnrollRuntime(context.Context, string, string) (sandboxbootstrap.Resource, error)
}

// EnrollmentHandler is part of our daemon connection contract, not an upstream
// Agents resource. It grants no Session API access and never issues another
// key: the enrolled sandbox serves its Link resource with the executor
// credential. A self_hosted sandbox dials the Link from its own host, so an
// origin without a wss Link enrolls none.
func EnrollmentHandler(s EnrollmentStore, origin deployment.PublicOrigin) http.Handler {
	link, err := origin.SandboxLink()
	if err == nil && !strings.HasPrefix(link, "wss:") {
		err = deployment.ErrNoLink
	}
	noLink := err != nil
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
		if noLink {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(struct {
				Error  string `json:"error"`
				Detail string `json:"detail"`
			}{"no_sandbox_link", "a self_hosted sandbox needs an https public URL"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		resource, err := s.EnrollRuntime(ctx, input.EnvironmentID, runtimedevice.HashCredential(authorization[1]))
		switch {
		case errors.Is(err, sessions.ErrNotFound):
			fail(http.StatusUnauthorized)
		case errors.Is(err, sessions.ErrDeviceBindingConflict):
			fail(http.StatusConflict)
		case err != nil:
			fail(http.StatusServiceUnavailable)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(struct {
				LinkURL  string                    `json:"link_url"`
				Resource sandboxbootstrap.Resource `json:"resource"`
			}{link, resource})
		}
	})
}
