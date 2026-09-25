package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/writeaudit"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// ProjectAPIKeyResolver resolves current database credentials on each request.
type ProjectAPIKeyResolver interface {
	ResolveProjectAPIKey(context.Context, string) (store.ProjectAPIKeyBinding, error)
}
type Authenticator struct{ keys ProjectAPIKeyResolver }

func NewDatabaseAuthenticator(keys ProjectAPIKeyResolver) (*Authenticator, error) {
	if keys == nil {
		return nil, errors.New("database API key resolver is required")
	}
	return &Authenticator{keys: keys}, nil
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
	if h.deploymentAuth != nil {
		if _, admin := h.deploymentAuth.digests[digest]; admin {
			return identity.Principal{}, writeaudit.Source{}, false, nil
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	binding, err := h.auth.keys.ResolveProjectAPIKey(ctx, hex.EncodeToString(digest[:]))
	if errors.Is(err, store.ErrNotFound) {
		return identity.Principal{}, writeaudit.Source{}, false, nil
	}
	if err != nil {
		return identity.Principal{}, writeaudit.Source{}, false, err
	}
	if !principalScopeHeaders(r, binding.Principal) {
		return identity.Principal{}, writeaudit.Source{}, false, nil
	}
	source := writeaudit.Source{KeyID: binding.Key.ID, Name: binding.Key.Name, Prefix: binding.Key.Prefix, Kind: "issued", TenantID: binding.Principal.TenantID}
	return binding.Principal, source, true, nil
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

// authenticateProject guards Files and Skills, which ignore OpenAI-Beta. As
// observed on them (HP-07), a 401 has a null code without a Bearer credential
// and invalid_api_key for a rejected one.
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
	if tenant, ok := r.Context().Value(adminTenantContextKey{}).(string); ok {
		return tenant
	}
	return r.Context().Value(principalContextKey{}).(identity.Principal).TenantID
}

func sessionCreator(r *http.Request) identity.Subject {
	return r.Context().Value(principalContextKey{}).(identity.Principal).Subject()
}
