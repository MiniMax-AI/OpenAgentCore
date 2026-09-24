package api

import (
	"context"
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

// adminTenantContextKey identifies an explicit management target, not a caller.
// Only deployment-authenticated, allowlisted resource handlers receive it.
type adminTenantContextKey struct{}

type AdminManagementStore interface {
	CopyAssets(context.Context, string, string, store.CopyAssetsInput) (store.CopyAssetsResult, error)
	ReadAdminSummary(context.Context, string, store.AdminSummaryFilter, func(store.Session) error) (store.AdminAssetCounts, error)
	ListAdminRuntimeTargets(context.Context, []string, string, int, bool) (store.AdminRuntimeTargetPage, error)
	ListAdminAudit(context.Context, store.AdminAuditFilter) (store.AdminAuditPage, error)
}

func WithAdminManagement(s AdminManagementStore) Option {
	return func(h *Handler) { h.adminManagement = s }
}

func (h *Handler) adminResourceScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		binding, ok := h.adminKeyScope(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminTenantContextKey{}, binding.Principal.TenantID)))
	})
}

func (h *Handler) registerAdminResourceRoutes(router chi.Router) {
	if h.deploymentAuth == nil || h.projectKeys == nil {
		return
	}
	router.Group(func(r chi.Router) {
		r.Use(h.deploymentAuth.authenticate)
		r.Get("/core/v1/admin/startup-configuration", h.adminStartupConfiguration)
		r.Get("/core/v1/admin/runtime-history/capabilities", h.adminRuntimeHistoryCapabilities)
		if h.adminManagement != nil {
			r.Post("/core/v1/admin/copies", h.copyAdminAssets)
			r.Get("/core/v1/admin/summary", h.adminSummary)
			r.Get("/core/v1/admin/runtime-observations", h.adminRuntimeObservations)
			r.Head("/core/v1/admin/runtime-observations", methodNotAllowed)
			r.Get("/core/v1/admin/audit-log", h.listAdminAudit)
		}
		r.Group(func(r chi.Router) {
			r.Use(h.adminResourceScope)
			r.Get("/core/v1/admin/api-keys/{key_id}/agents", h.adminListAgents)
			r.Get("/core/v1/admin/api-keys/{key_id}/agents/{agent_id}", h.adminGetAgent)
			r.Delete("/core/v1/admin/api-keys/{key_id}/agents/{agent_id}", h.adminDeleteAgent)
			r.Get("/core/v1/admin/api-keys/{key_id}/environment-templates", h.adminListEnvironmentTemplates)
			r.Get("/core/v1/admin/api-keys/{key_id}/environment-templates/{environment_template_id}", h.adminGetEnvironmentTemplate)
			r.Delete("/core/v1/admin/api-keys/{key_id}/environment-templates/{environment_template_id}", h.adminDeleteEnvironmentTemplate)
			r.Get("/core/v1/admin/api-keys/{key_id}/skills", h.adminListSkills)
			r.Get("/core/v1/admin/api-keys/{key_id}/skills/{skill_id}", h.adminGetSkill)
			r.Delete("/core/v1/admin/api-keys/{key_id}/skills/{skill_id}", h.adminDeleteSkill)
			r.Get("/core/v1/admin/api-keys/{key_id}/skills/{skill_id}/content", h.adminSkillContent)
			r.Head("/core/v1/admin/api-keys/{key_id}/skills/{skill_id}/content", methodNotAllowed)
			r.Get("/core/v1/admin/api-keys/{key_id}/skills/{skill_id}/versions", h.adminListSkillVersions)
			r.Get("/core/v1/admin/api-keys/{key_id}/skills/{skill_id}/versions/{version}", h.adminGetSkillVersion)
			r.Delete("/core/v1/admin/api-keys/{key_id}/skills/{skill_id}/versions/{version}", h.adminDeleteSkillVersion)
			r.Get("/core/v1/admin/api-keys/{key_id}/skills/{skill_id}/versions/{version}/content", h.adminSkillVersionContent)
			r.Head("/core/v1/admin/api-keys/{key_id}/skills/{skill_id}/versions/{version}/content", methodNotAllowed)
			r.Get("/core/v1/admin/api-keys/{key_id}/files", h.adminListSourceFiles)
			r.Get("/core/v1/admin/api-keys/{key_id}/files/{file_id}", h.adminGetSourceFile)
			r.Delete("/core/v1/admin/api-keys/{key_id}/files/{file_id}", h.adminDeleteSourceFile)
			r.Get("/core/v1/admin/api-keys/{key_id}/vaults", h.adminListVaults)
			r.Get("/core/v1/admin/api-keys/{key_id}/vaults/{vault_id}", h.adminGetVault)
			r.Delete("/core/v1/admin/api-keys/{key_id}/vaults/{vault_id}", h.adminDeleteVault)
			r.Get("/core/v1/admin/api-keys/{key_id}/vaults/{vault_id}/credentials", h.adminListCredentials)
			r.Get("/core/v1/admin/api-keys/{key_id}/vaults/{vault_id}/credentials/{credential_id}", h.adminGetCredential)
			r.Delete("/core/v1/admin/api-keys/{key_id}/vaults/{vault_id}/credentials/{credential_id}", h.adminDeleteCredential)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions", h.adminListSessions)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}", h.adminGetSession)
			r.Delete("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}", h.adminDeleteSession)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/turns", h.adminListTurns)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/turns/{turn_id}", h.adminGetTurn)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/items", h.adminListItems)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/artifacts", h.adminListSessionArtifacts)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/artifacts/{artifact_id}", h.adminGetSessionArtifact)
			r.Delete("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/artifacts/{artifact_id}", h.adminDeleteSessionArtifact)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/artifacts/{artifact_id}/content", h.adminSessionArtifactContent)
			r.Head("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/artifacts/{artifact_id}/content", methodNotAllowed)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/execution-configuration", h.adminGetSessionExecutionConfiguration)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/runtime-observation", h.adminGetRuntimeObservation)
			r.Head("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/runtime-observation", methodNotAllowed)
			r.Get("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/runtime-history", h.adminGetRuntimeHistory)
			r.Head("/core/v1/admin/api-keys/{key_id}/sessions/{session_id}/runtime-history", methodNotAllowed)
			if h.writeAudit != nil {
				r.Get("/core/v1/admin/api-keys/{key_id}/resource-owners", h.getResourceOwners)
				r.Get("/core/v1/admin/api-keys/{key_id}/write-operations", h.listWriteOperations)
			}
		})
	})
}

// @Summary Retrieve deployment startup metadata
// @Description Deployment administrator only. Returns the same non-secret configuration metadata as the project extension.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} v1.CoreStartupConfiguration
// @Failure 401,503 {object} v1.ErrorResponse
// @Router /core/v1/admin/startup-configuration [get]
func (h *Handler) adminStartupConfiguration(w http.ResponseWriter, r *http.Request) {
	h.getStartupConfiguration(w, r)
}

// @Summary Retrieve durable Runtime history capabilities
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Success 200 {object} v1.RuntimeHistoryCapabilities
// @Failure 401 {object} v1.ErrorResponse
// @Router /core/v1/admin/runtime-history/capabilities [get]
func (h *Handler) adminRuntimeHistoryCapabilities(w http.ResponseWriter, r *http.Request) {
	h.getRuntimeHistoryCapabilities(w, r)
}
