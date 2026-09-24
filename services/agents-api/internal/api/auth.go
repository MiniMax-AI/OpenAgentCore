package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/writeaudit"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// APIKey binds a service credential to an execution principal, not a product user.
// Configuration stores the SHA-256 hex digest, never the plaintext key.
type APIKey struct {
	Name           string `json:"name,omitempty"`
	Kind           string `json:"kind,omitempty"`
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
	sources    map[[32]byte]writeaudit.Source
}

func NewAuthenticator(keys []APIKey) (*Authenticator, error) {
	if len(keys) == 0 {
		return nil, errors.New("at least one Agents API key is required")
	}
	a := &Authenticator{principals: make(map[[32]byte]identity.Principal, len(keys)), sources: make(map[[32]byte]writeaudit.Source, len(keys))}
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
		if key.Kind == "" {
			key.Kind = "static"
		}
		if key.Kind != "static" && key.Kind != "console" {
			return nil, errors.New("configured API key kind must be static or console")
		}
		if !utf8.ValidString(key.Name) || utf8.RuneCountInString(key.Name) > 80 || strings.ContainsFunc(key.Name, unicode.IsControl) {
			return nil, errors.New("configured API key name must contain at most 80 characters without controls")
		}
		digestID := hex.EncodeToString(hash[:])
		a.sources[hash] = writeaudit.Source{KeyID: "static:" + digestID, Name: strings.TrimSpace(key.Name), Prefix: digestID[:8], Kind: key.Kind, TenantID: principal.TenantID}
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
	principal, _, ok, err := h.resolveCaller(r)
	return principal, ok, err
}

func (h *Handler) resolveCaller(r *http.Request) (identity.Principal, writeaudit.Source, bool, error) {
	digest, valid := projectBearerDigest(r)
	if !valid {
		return identity.Principal{}, writeaudit.Source{}, false, nil
	}
	if principal, ok := h.auth.principals[digest]; ok {
		return principal, h.auth.sources[digest], principalScopeHeaders(r, principal), nil
	}
	if h.projectKeys == nil {
		return identity.Principal{}, writeaudit.Source{}, false, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	binding, err := h.projectKeys.ResolveProjectAPIKey(ctx, hex.EncodeToString(digest[:]))
	if errors.Is(err, store.ErrNotFound) {
		return identity.Principal{}, writeaudit.Source{}, false, nil
	}
	if err != nil {
		return identity.Principal{}, writeaudit.Source{}, false, err
	}
	parent, ok := h.auth.staticBinding(binding.BindingDigest)
	if !ok || parent != binding.Principal || !principalScopeHeaders(r, parent) {
		return identity.Principal{}, writeaudit.Source{}, false, nil
	}
	source := writeaudit.Source{KeyID: binding.Key.ID, Name: binding.Key.Name, Prefix: binding.Key.Prefix, Kind: "issued", TenantID: parent.TenantID}
	return parent, source, true, nil
}

func matchesScopeHeader(r *http.Request, name, expected string) bool {
	values := r.Header.Values(name)
	return len(values) == 0 || (len(values) == 1 && values[0] == expected)
}

type principalContextKey struct{}

const invalidBetaMessage = "To access the Agents API, set the 'OpenAI-Beta' header to 'agents=v1'."

// authenticate guards the Beta group. As observed officially (HP-05), the
// constant OpenAI-Beta check runs first: it reads only that header, and its
// 400 carries no tenant or resource data. Every request that passes it is
// authenticated before routing reaches any handler, including the group's 404
// and 405 responses. The header must have exactly one field value (HP-03).
// Every Beta 401 has a null code (HP-07).
func (h *Handler) authenticate(next http.Handler) http.Handler {
	authenticated := h.authenticateCaller(next, false)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values("OpenAI-Beta"); len(values) != 1 || values[0] != "agents=v1" {
			writeError(w, http.StatusBadRequest, "invalid_beta", invalidBetaMessage)
			return
		}
		authenticated.ServeHTTP(w, r)
	})
}

// authenticateProject guards Files, Skills and Core project extensions, which
// ignore OpenAI-Beta. As observed on Files and Skills (HP-07), a 401 has a null
// code without a Bearer credential and invalid_api_key for a rejected one.
func (h *Handler) authenticateProject(next http.Handler) http.Handler {
	return h.authenticateCaller(next, true)
}

func (h *Handler) authenticateCaller(next http.Handler, reportInvalidKey bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, source, ok, err := h.resolveCaller(r)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "authentication_unavailable", "API key authentication is temporarily unavailable.")
			return
		}
		if !ok {
			code := ""
			// A Bearer credential was supplied: exactly one Authorization header
			// in the Bearer scheme, rejected by key or scope headers.
			if _, bearer := projectBearerDigest(r); reportInvalidKey && bearer {
				code = "invalid_api_key"
			}
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, code, "A valid Agents API bearer key is required.")
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		if strings.HasPrefix(r.URL.Path, "/v1/") && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
			source.RequestID, _ = log.RequestIDFromContext(ctx)
			if carrier, ok := log.TraceFromContext(ctx); ok {
				source.TraceID = carrier.Trace.String()
			}
			ctx = writeaudit.WithSource(ctx, source)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func tenantID(r *http.Request) string {
	return r.Context().Value(principalContextKey{}).(identity.Principal).TenantID
}

func sessionCreator(r *http.Request) identity.Subject {
	return r.Context().Value(principalContextKey{}).(identity.Principal).Subject()
}
