package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// APIKey binds a service credential to an execution principal, not a product user.
// Configuration stores the SHA-256 hex digest, never the plaintext key.
type APIKey struct {
	TokenSHA256    string `json:"token_sha256"`
	TenantID       string `json:"tenant_id"`
	OrganizationID string `json:"organization_id"`
	ProjectID      string `json:"project_id"`
	SubjectKind    string `json:"subject_kind"`
	SubjectID      string `json:"subject_id"`
}

type Authenticator struct {
	principals map[[32]byte]identity.Principal
	projects   []identity.ProjectScope
}

func NewAuthenticator(keys []APIKey) (*Authenticator, error) {
	if len(keys) == 0 {
		return nil, errors.New("at least one Agents API key is required")
	}
	a := &Authenticator{principals: make(map[[32]byte]identity.Principal, len(keys))}
	for _, key := range keys {
		principal := identity.Principal{ProjectScope: identity.ProjectScope{TenantID: key.TenantID, OrganizationID: key.OrganizationID, ProjectID: key.ProjectID}, SubjectKind: key.SubjectKind, SubjectID: key.SubjectID}
		if err := principal.Validate(); err != nil {
			return nil, err
		}
		digest, err := hex.DecodeString(key.TokenSHA256)
		if err != nil || len(digest) != sha256.Size {
			return nil, errors.New("API key token_sha256 must be a SHA-256 hex digest")
		}
		hash := [32]byte(digest)
		if _, exists := a.principals[hash]; exists {
			return nil, errors.New("duplicate API key digest")
		}
		a.principals[hash] = principal
		a.projects = append(a.projects, principal.ProjectScope)
	}
	var err error
	a.projects, err = identity.ProjectScopes(a.projects)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Authenticator) ProjectScopes() []identity.ProjectScope {
	return append([]identity.ProjectScope(nil), a.projects...)
}

func (a *Authenticator) principal(r *http.Request) (identity.Principal, bool) {
	digest, valid := projectBearerDigest(r)
	if !valid {
		return identity.Principal{}, false
	}
	principal, ok := a.principals[digest]
	return principal, ok && principalScopeHeaders(r, principal)
}

func projectBearerDigest(r *http.Request) ([sha256.Size]byte, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(r.Header.Values("Authorization")) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256([]byte(parts[1])), true
}

func principalScopeHeaders(r *http.Request, principal identity.Principal) bool {
	return matchesScopeHeader(r, "OpenAI-Organization", principal.OrganizationID) && matchesScopeHeader(r, "OpenAI-Project", principal.ProjectID)
}

func (h *Handler) resolvePrincipal(r *http.Request) (identity.Principal, bool, error) {
	digest, valid := projectBearerDigest(r)
	if !valid {
		return identity.Principal{}, false, nil
	}
	if principal, ok := h.auth.principals[digest]; ok {
		return principal, principalScopeHeaders(r, principal), nil
	}
	if h.projectKeys == nil {
		return identity.Principal{}, false, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	binding, err := h.projectKeys.ResolveProjectAPIKey(ctx, hex.EncodeToString(digest[:]))
	if errors.Is(err, store.ErrNotFound) {
		return identity.Principal{}, false, nil
	}
	if err != nil {
		return identity.Principal{}, false, err
	}
	parent, ok := h.auth.staticBinding(binding.BindingDigest)
	if !ok || parent != binding.Principal || !principalScopeHeaders(r, parent) {
		return identity.Principal{}, false, nil
	}
	return parent, true, nil
}

func matchesScopeHeader(r *http.Request, name, expected string) bool {
	values := r.Header.Values(name)
	return len(values) == 0 || (len(values) == 1 && values[0] == expected)
}

type principalContextKey struct{}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return h.authenticateProject(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenAI-Beta") != "agents=v1" {
			writeError(w, http.StatusBadRequest, "invalid_beta", "OpenAI-Beta: agents=v1 is required.")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (h *Handler) authenticateProject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok, err := h.resolvePrincipal(r)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "authentication_unavailable", "API key authentication is temporarily unavailable.")
			return
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "A valid Agents API bearer key is required.")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
	})
}

func tenantID(r *http.Request) string {
	return r.Context().Value(principalContextKey{}).(identity.Principal).TenantID
}

func sessionCreator(r *http.Request) identity.Subject {
	return r.Context().Value(principalContextKey{}).(identity.Principal).Subject()
}
