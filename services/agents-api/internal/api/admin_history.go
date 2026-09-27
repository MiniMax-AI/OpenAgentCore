package api

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// @Summary Query committed administrator mutations
// @Description Core key only. Newest-first cursor pagination of safe metadata. Actor labels are unverified console labels, not authorization identities. Request bodies and secrets are never recorded.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id query string false "Target Project ID"
// @Param resource_type query string false "Resource type"
// @Param resource_id query string false "Resource ID"
// @Param action query string false "Mutation action"
// @Param created_after query string false "Inclusive RFC3339 timestamp"
// @Param created_before query string false "Exclusive RFC3339 timestamp"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(50)
// @Param after query string false "Opaque next_cursor from the preceding page"
// @Success 200 {object} store.AdminAuditPage
// @Failure 400,401,500 {object} CoreErrorResponse
// @Router /core/v1/audit-log [get]
func (h *Handler) listAdminAudit(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	for name, entries := range values {
		switch name {
		case "project_id", "resource_type", "resource_id", "action", "created_after", "created_before", "limit", "after":
		default:
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
		if len(entries) != 1 || entries[0] == "" {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
	}
	filter := store.AdminAuditFilter{ProjectID: values.Get("project_id"), ResourceType: values.Get("resource_type"), ResourceID: values.Get("resource_id"), Action: values.Get("action"), After: values.Get("after"), Limit: 50}
	if limit := values.Get("limit"); limit != "" {
		filter.Limit, err = strconv.Atoi(limit)
		if err != nil || filter.Limit < 1 || filter.Limit > 100 {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
	}
	filter.CreatedAfter, err = adminSummaryTime(r, "created_after")
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	filter.CreatedBefore, err = adminSummaryTime(r, "created_before")
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	page, err := h.adminManagement.ListAdminAudit(r.Context(), filter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
