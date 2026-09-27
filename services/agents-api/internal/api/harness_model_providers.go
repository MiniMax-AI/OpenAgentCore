package api

import (
	"context"
	"net/http"
	"slices"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

// DeploymentModelProviderStore holds one deployment default model provider per
// harness. Keys are write-only and encrypted.
type DeploymentModelProviderStore interface {
	ListDeploymentModelProviders(context.Context) ([]store.DeploymentModelProvider, error)
	SetDeploymentModelProvider(context.Context, string, v1.ModelProviderInput) (store.DeploymentModelProvider, error)
	DeleteDeploymentModelProvider(context.Context, string) error
}

// HarnessModelProvider is a harness's deployment default model provider. It
// never contains the API key, only whether one is configured.
type HarnessModelProvider struct {
	Object           string    `json:"object" enums:"core.model_provider" binding:"required"`
	Harness          string    `json:"harness" enums:"claude_sdk,codex,mcode" binding:"required"`
	Protocol         string    `json:"protocol" enums:"anthropic,responses" binding:"required"`
	BaseURL          string    `json:"base_url" binding:"required"`
	ContextWindow    int32     `json:"context_window,omitempty"`
	MaxOutputTokens  int32     `json:"max_output_tokens,omitempty"`
	APIKeyConfigured bool      `json:"api_key_configured" binding:"required"`
	UpdatedAt        time.Time `json:"updated_at" binding:"required"`
}

// CoreHarness describes one harness this build supports. Enabled and default
// come from the process configuration; the model provider is the deployment
// default stored in Core, or null.
type CoreHarness struct {
	Object        string                `json:"object" enums:"core.harness" binding:"required"`
	ID            string                `json:"id" enums:"claude_sdk,codex,mcode" binding:"required"`
	Enabled       bool                  `json:"enabled" binding:"required"`
	Default       bool                  `json:"default" binding:"required"`
	ModelProvider *HarnessModelProvider `json:"model_provider" extensions:"x-nullable" binding:"required"`
}

type CoreHarnessList struct {
	Object string        `json:"object" enums:"list" binding:"required"`
	Data   []CoreHarness `json:"data" binding:"required"`
}

func harnessModelProvider(value store.DeploymentModelProvider) *HarnessModelProvider {
	return &HarnessModelProvider{Object: "core.model_provider", Harness: value.Harness,
		Protocol: value.Provider.Protocol, BaseURL: value.Provider.BaseURL,
		ContextWindow: value.Provider.ContextWindow, MaxOutputTokens: value.Provider.MaxOutputTokens,
		APIKeyConfigured: value.Provider.APIKeyConfigured, UpdatedAt: value.UpdatedAt.UTC()}
}

// registerHarnessRoutes adds harness and deployment model provider management
// to the Core-key-authenticated /core/v1 router.
func (h *Handler) registerHarnessRoutes(r chi.Router) {
	s, ok := h.store.(DeploymentModelProviderStore)
	if !ok {
		return
	}
	r.Get("/harnesses", func(w http.ResponseWriter, r *http.Request) { h.listHarnesses(w, r, s) })
	r.Get("/harnesses/{harness}/model-provider", func(w http.ResponseWriter, r *http.Request) { h.getHarnessModelProvider(w, r, s) })
	r.Put("/harnesses/{harness}/model-provider", func(w http.ResponseWriter, r *http.Request) { h.setHarnessModelProvider(w, r, s) })
	r.Delete("/harnesses/{harness}/model-provider", func(w http.ResponseWriter, r *http.Request) { h.deleteHarnessModelProvider(w, r, s) })
}

// knownHarness reports the path harness, writing 404 for one this build lacks.
func knownHarness(w http.ResponseWriter, r *http.Request) (string, bool) {
	harness := chi.URLParam(r, "harness")
	if !slices.Contains((engine.Catalog{}).Kinds(), harness) {
		writeError(w, http.StatusNotFound, "not_found", "This harness does not exist.")
		return "", false
	}
	return harness, true
}

// @Summary List harnesses and their deployment default model providers
// @Description Core key only. Returns every harness this build supports, in name order. enabled and default are read-only views of the process configuration (OAC_DEFAULT_HARNESS and OAC_HARNESSES). model_provider is the harness's deployment default, stored in Core, or null. Keys are never returned; api_key_configured reports that one is set.
// @Tags Deployment Model Providers
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} api.CoreHarnessList
// @Failure 401,500 {object} v1.ErrorResponse
// @Router /core/v1/harnesses [get]
func (h *Handler) listHarnesses(w http.ResponseWriter, r *http.Request, s DeploymentModelProviderStore) {
	providers, err := s.ListDeploymentModelProviders(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	list := CoreHarnessList{Object: "list", Data: []CoreHarness{}}
	for _, kind := range (engine.Catalog{}).Kinds() {
		harness := CoreHarness{Object: "core.harness", ID: kind, Enabled: kind == h.engine || h.harnesses[kind], Default: kind == h.engine}
		for _, provider := range providers {
			if provider.Harness == kind {
				harness.ModelProvider = harnessModelProvider(provider)
			}
		}
		list.Data = append(list.Data, harness)
	}
	writeJSON(w, http.StatusOK, list)
}

// @Summary Retrieve a harness's deployment default model provider
// @Description Core key only. Returns the safe view; the key is never returned. 404 when the harness does not exist or has no deployment default.
// @Tags Deployment Model Providers
// @Produce json
// @Security DeploymentAdminAuth
// @Param harness path string true "Harness" Enums(claude_sdk,codex,mcode)
// @Success 200 {object} api.HarnessModelProvider
// @Failure 401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/harnesses/{harness}/model-provider [get]
func (h *Handler) getHarnessModelProvider(w http.ResponseWriter, r *http.Request, s DeploymentModelProviderStore) {
	harness, ok := knownHarness(w, r)
	if !ok {
		return
	}
	providers, err := s.ListDeploymentModelProviders(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	for _, provider := range providers {
		if provider.Harness == harness {
			writeJSON(w, http.StatusOK, harnessModelProvider(provider))
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "This harness has no deployment default model provider.")
}

var harnessModelProviderShape = shape{kind: objectValue, members: []member{
	{"protocol", requiredString}, {"base_url", requiredString}, {"api_key", requiredString},
	{"context_window", shape{kind: integerValue, minimum: 0}},
	{"max_output_tokens", shape{kind: integerValue, minimum: 0}},
}}

// @Summary Replace a harness's deployment default model provider
// @Description Core key only. The body is the complete x_agents_core.model_provider bundle, including the write-only api_key; there is no partial update and bundles are never merged. The provider is validated for this harness: an HTTPS base_url without credentials, query or fragment, the harness's protocol (responses for codex, anthropic for claude_sdk and mcode) and, for mcode, positive context_window and max_output_tokens. New openai_hosted and none Sessions that resolve no Session or Agent bundle freeze this default into their encrypted snapshot; existing Sessions never change. self_hosted Sessions never use it. The key is encrypted and never returned. Each write records an administrator audit entry without the key.
// @Tags Deployment Model Providers
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param harness path string true "Harness" Enums(claude_sdk,codex,mcode)
// @Param body body v1.ModelProviderInput true "Complete model provider bundle"
// @Success 200 {object} api.HarnessModelProvider
// @Failure 400,401,404,413,500,503 {object} v1.ErrorResponse
// @Router /core/v1/harnesses/{harness}/model-provider [put]
func (h *Handler) setHarnessModelProvider(w http.ResponseWriter, r *http.Request, s DeploymentModelProviderStore) {
	harness, ok := knownHarness(w, r)
	if !ok {
		return
	}
	raw, ok := readJSONObjectLimit(w, r, 32*1024, "Request exceeds 32 KiB.")
	if !ok {
		return
	}
	var input v1.ModelProviderInput
	// Neither message echoes submitted values, which may include the key.
	if checkValue("model_provider", raw, harnessModelProviderShape) != nil || decodeInputObject(raw, &input, "protocol", "base_url", "api_key", "context_window", "max_output_tokens") != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "The body must be a complete model provider: protocol, base_url, api_key and optional nonnegative context_window and max_output_tokens.")
		return
	}
	if err := input.ValidateHarness(harness); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	setAdminAuditSource(r, "")
	provider, err := s.SetDeploymentModelProvider(r.Context(), harness, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, harnessModelProvider(provider))
}

// @Summary Remove a harness's deployment default model provider
// @Description Core key only. Idempotent; each successful request is audited. Sessions that already froze the default keep it. Afterwards new openai_hosted Sessions for this harness need a Session or Agent bundle.
// @Tags Deployment Model Providers
// @Security DeploymentAdminAuth
// @Param harness path string true "Harness" Enums(claude_sdk,codex,mcode)
// @Success 204
// @Failure 401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/harnesses/{harness}/model-provider [delete]
func (h *Handler) deleteHarnessModelProvider(w http.ResponseWriter, r *http.Request, s DeploymentModelProviderStore) {
	harness, ok := knownHarness(w, r)
	if !ok {
		return
	}
	setAdminAuditSource(r, "")
	if err := s.DeleteDeploymentModelProvider(r.Context(), harness); err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
