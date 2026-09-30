package api

import (
	"context"
	"net/http"
	"slices"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
)

// DeploymentModelProviderStore holds one deployment default model provider per
// harness. Keys are write-only and encrypted.
type DeploymentModelProviderStore interface {
	ListDeploymentModelProviders(context.Context) ([]store.DeploymentModelProvider, error)
	SetDeploymentModelProvider(context.Context, string, v1.ModelConfigurationInput) (store.DeploymentModelProvider, error)
	DeleteDeploymentModelProvider(context.Context, string) error
}

// HarnessModelConfiguration is a harness's deployment default model provider. It
// never contains the API key, only whether one is configured.
type HarnessModelConfiguration struct {
	Object  string `json:"object" enums:"core.model_configuration" binding:"required"`
	Harness string `json:"harness" binding:"required"`
	v1.ModelConfigurationView
	LastUsedAt    *time.Time `json:"last_used_at" format:"date-time" extensions:"x-nullable" binding:"required"`
	LastErrorCode *string    `json:"last_error_code" extensions:"x-nullable" binding:"required" enums:"authentication_error,connection_failed,rate_limit_exceeded,usage_limit_exceeded,server_overloaded,server_error,resource_not_found,request_timeout,invalid_request"`
	LastErrorAt   *time.Time `json:"last_error_at" format:"date-time" extensions:"x-nullable" binding:"required"`
	UpdatedAt     time.Time  `json:"updated_at" binding:"required"`
}

// CoreHarness describes one harness this build supports. Enabled and default
// come from the process configuration; the model provider is the deployment
// default stored in Core, or null.
type CoreHarness struct {
	ModelConfigurationSupport v1.ModelConfigurationSupport `json:"model_configuration_support" binding:"required"`
	Object                    string                       `json:"object" enums:"core.harness" binding:"required"`
	ID                        string                       `json:"id" binding:"required"`
	Enabled                   bool                         `json:"enabled" binding:"required"`
	Default                   bool                         `json:"default" binding:"required"`
	ModelConfiguration        *HarnessModelConfiguration   `json:"model_configuration" extensions:"x-nullable" binding:"required"`
}

type CoreHarnessList struct {
	Object string        `json:"object" enums:"list" binding:"required"`
	Data   []CoreHarness `json:"data" binding:"required"`
}

func harnessModelConfiguration(value store.DeploymentModelProvider) *HarnessModelConfiguration {
	return &HarnessModelConfiguration{Object: "core.model_configuration", Harness: value.Harness,
		ModelConfigurationView: v1.ModelConfigurationView{ModelProvider: &value.Provider, Model: value.Model, HarnessConfig: value.HarnessConfig}, UpdatedAt: value.UpdatedAt.UTC(), LastUsedAt: value.LastUsedAt, LastErrorCode: value.LastErrorCode, LastErrorAt: value.LastErrorAt}
}

// registerHarnessRoutes adds harness and deployment model provider management
// to the Core-key-authenticated /core/v1 router.
func (h *Handler) registerHarnessRoutes(r chi.Router) {
	s, ok := h.store.(DeploymentModelProviderStore)
	if !ok {
		return
	}
	r.Get("/harnesses", func(w http.ResponseWriter, r *http.Request) { h.listHarnesses(w, r, s) })
	r.Get("/harnesses/{harness}/model-configuration", func(w http.ResponseWriter, r *http.Request) { h.getHarnessModelConfiguration(w, r, s) })
	r.Put("/harnesses/{harness}/model-configuration", func(w http.ResponseWriter, r *http.Request) { h.setHarnessModelConfiguration(w, r, s) })
	r.Delete("/harnesses/{harness}/model-configuration", func(w http.ResponseWriter, r *http.Request) { h.deleteHarnessModelConfiguration(w, r, s) })
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
// @Description Core key only. Returns every harness this build supports, in name order. enabled and default are read-only views of the process configuration (OAC_DEFAULT_HARNESS and OAC_HARNESSES). model_configuration is the harness's deployment default, stored in Core, or null. Keys are never returned; api_key_configured reports that one is set.
// @Tags Deployment Model Providers
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} api.CoreHarnessList
// @Failure 401,500 {object} CoreErrorResponse
// @Router /core/v1/harnesses [get]
func (h *Handler) listHarnesses(w http.ResponseWriter, r *http.Request, s DeploymentModelProviderStore) {
	providers, err := s.ListDeploymentModelProviders(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	list := CoreHarnessList{Object: "list", Data: []CoreHarness{}}
	for _, kind := range (engine.Catalog{}).Kinds() {
		declaration, _ := builtin.Registry().Lookup(kind)
		support := v1.ModelConfigurationSupport{Protocols: []string{}, AcceptsHarnessConfig: declaration.AcceptsHarnessConfig()}
		for _, provider := range declaration.Providers {
			support.Protocols = append(support.Protocols, provider.Protocol)
			support.TokenLimitsRequired = support.TokenLimitsRequired || provider.RequiresTokenLimits
		}
		harness := CoreHarness{ModelConfigurationSupport: support, Object: "core.harness", ID: kind, Enabled: kind == h.engine || h.harnesses[kind], Default: kind == h.engine}
		for _, provider := range providers {
			if provider.Harness == kind {
				harness.ModelConfiguration = harnessModelConfiguration(provider)
			}
		}
		list.Data = append(list.Data, harness)
	}
	writeJSON(w, http.StatusOK, list)
}

// @Summary Retrieve a harness's deployment default model provider
// @Description Core key only. Returns the safe view; the key is never returned. 404 when the harness does not exist or has no deployment default. Nullable last_used_at, last_error_code and last_error_at are best-effort observations of committed root Turns using this exact default revision; they do not establish current readiness and may remain stale indefinitely.
// @Tags Deployment Model Providers
// @Produce json
// @Security DeploymentAdminAuth
// @Param harness path string true "Harness"
// @Success 200 {object} api.HarnessModelConfiguration
// @Failure 401,404,500 {object} CoreErrorResponse
// @Router /core/v1/harnesses/{harness}/model-configuration [get]
func (h *Handler) getHarnessModelConfiguration(w http.ResponseWriter, r *http.Request, s DeploymentModelProviderStore) {
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
			writeJSON(w, http.StatusOK, harnessModelConfiguration(provider))
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "This harness has no deployment default model provider.")
}

var modelProviderInputShape = shape{kind: objectValue, members: []member{
	{"protocol", requiredString}, {"base_url", requiredString}, {"api_key", requiredString},
	{"context_window", shape{kind: integerValue, minimum: 0}},
	{"max_output_tokens", shape{kind: integerValue, minimum: 0}},
}}

var modelConfigurationShape = shape{kind: objectValue, members: []member{
	{"model_provider", requiredModelProviderShape()}, {"model", requiredString},
	{"harness_config", shape{kind: openObject}},
}}

func requiredModelProviderShape() shape {
	result := modelProviderInputShape
	result.required = true
	return result
}

// @Summary Replace a harness's deployment default model provider
// @Description Core key only. Replaces one complete deployment model configuration, including its write-only provider key. Validates through the selected Harness declaration and freezes the resolved configuration for new Sessions; existing Sessions are unchanged. See contracts/agents-api/model-execution.md#deployment-defaults for fields, source precedence and observation rules.
// @Tags Deployment Model Providers
// @Accept json
// @Produce json
// @Security DeploymentAdminAuth
// @Param harness path string true "Harness"
// @Param body body v1.ModelConfigurationInput true "Complete model provider bundle"
// @Success 200 {object} api.HarnessModelConfiguration
// @Failure 400,401,404,413,500,503 {object} CoreErrorResponse
// @Router /core/v1/harnesses/{harness}/model-configuration [put]
func (h *Handler) setHarnessModelConfiguration(w http.ResponseWriter, r *http.Request, s DeploymentModelProviderStore) {
	harness, ok := knownHarness(w, r)
	if !ok {
		return
	}
	raw, ok := readJSONObjectLimit(w, r, 32*1024, "Request exceeds 32 KiB.")
	if !ok {
		return
	}
	var input v1.ModelConfigurationInput
	// Neither message echoes submitted values, which may include the key.
	if checkValue("model_configuration", raw, modelConfigurationShape) != nil || decodeInputObject(raw, &input, "model_provider", "model", "harness_config") != nil {
		writeError(w, http.StatusBadRequest, "invalid_model_provider", "The body requires model_provider, model and optional harness_config.")
		return
	}
	if err := input.ValidateHarness(harness); err != nil {
		if writeCoreModelProviderError(w, err, harness) {
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	setAdminAuditSource(r, "")
	provider, err := s.SetDeploymentModelProvider(r.Context(), harness, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, harnessModelConfiguration(provider))
}

// @Summary Remove a harness's deployment default model provider
// @Description Core key only. Idempotent; each successful request is audited. Sessions that already froze the default keep it. Afterwards new openai_hosted Sessions for this harness need a Session or Agent bundle.
// @Tags Deployment Model Providers
// @Security DeploymentAdminAuth
// @Param harness path string true "Harness"
// @Success 204
// @Failure 401,404,500 {object} CoreErrorResponse
// @Router /core/v1/harnesses/{harness}/model-configuration [delete]
func (h *Handler) deleteHarnessModelConfiguration(w http.ResponseWriter, r *http.Request, s DeploymentModelProviderStore) {
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
