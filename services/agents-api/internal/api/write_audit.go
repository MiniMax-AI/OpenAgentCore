package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// WriteAuditStore exposes safe read models, never request bodies or credentials.
type WriteAuditStore interface {
	GetResourceOwners(context.Context, string, string, []string) ([]store.ResourceOwner, error)
	ListWriteOperations(context.Context, string, store.WriteOperationFilter) (store.WriteOperationPage, error)
}

type ResourceOwnerList struct {
	Data []store.ResourceOwner `json:"data"`
}

func WithWriteAudit(s WriteAuditStore, auth *DeploymentAuthenticator) Option {
	return func(h *Handler) {
		h.writeAudit = s
		if auth != nil {
			h.deploymentAuth = auth
		}
	}
}

func (h *Handler) writeAuditScope(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, string, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return nil, "", false
	}
	for name, entries := range values {
		recognized := false
		for _, field := range allowed {
			if name == field {
				recognized = true
			}
		}
		if !recognized || len(entries) != 1 || entries[0] == "" {
			writeStoreError(w, r, store.ErrInvalidInput)
			return nil, "", false
		}
	}
	tenant, ok := r.Context().Value(adminTenantContextKey{}).(string)
	if !ok || tenant == "" {
		writeStoreError(w, r, store.ErrNotFound)
		return nil, "", false
	}
	return values, tenant, true
}

// @Summary Batch lookup resource creation keys
// @Description Deployment administrator only. The key path selects its independent space. Returns null for resources without recorded creation provenance, including historical and foreign resources. No key secret is returned.
// @Tags Write Audit
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project ID"
// @Param resource_type query string true "Resource type"
// @Param resource_ids query string true "Comma-separated public resource IDs, maximum 100"
// @Success 200 {object} api.ResourceOwnerList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/admin/projects/{project_id}/resource-owners [get]
func (h *Handler) getResourceOwners(w http.ResponseWriter, r *http.Request) {
	values, tenant, ok := h.writeAuditScope(w, r, "resource_type", "resource_ids")
	if !ok {
		return
	}
	ids := strings.Split(values.Get("resource_ids"), ",")
	if !store.ValidAuditResourceType(values.Get("resource_type")) || len(ids) > 100 {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	for _, id := range ids {
		if id == "" || len(id) > 256 || strings.TrimSpace(id) != id {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
	}
	owners, err := h.writeAudit.GetResourceOwners(r.Context(), tenant, values.Get("resource_type"), ids)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if owners == nil {
		owners = []store.ResourceOwner{}
	}
	writeJSON(w, http.StatusOK, ResourceOwnerList{Data: owners})
}

// @Summary Query API-key write operations
// @Description Deployment administrator only. Reverse chronological keyset pagination over committed writes. Creation records remain; other records follow configured retention. The key path selects its independent space, never a caller-supplied tenant.
// @Tags Write Audit
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project ID"
// @Param key_id query string false "Recorded creator key ID"
// @Param resource_type query string false "Resource type"
// @Param resource_id query string false "Public resource ID"
// @Param created_after query string false "Inclusive RFC3339 timestamp"
// @Param created_before query string false "Exclusive RFC3339 timestamp"
// @Param limit query int false "Page size, 1-100, default 50"
// @Param after query string false "Opaque next_cursor from the preceding page"
// @Success 200 {object} store.WriteOperationPage
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/admin/projects/{project_id}/write-operations [get]
func (h *Handler) listWriteOperations(w http.ResponseWriter, r *http.Request) {
	values, tenant, ok := h.writeAuditScope(w, r, "key_id", "resource_type", "resource_id", "created_after", "created_before", "limit", "after")
	if !ok {
		return
	}
	filter := store.WriteOperationFilter{KeyID: values.Get("key_id"), ResourceType: values.Get("resource_type"), ResourceID: values.Get("resource_id"), After: values.Get("after"), Limit: 50}
	if filter.ResourceType != "" && !store.ValidAuditResourceType(filter.ResourceType) {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	for _, value := range []string{filter.KeyID, filter.ResourceID} {
		if len(value) > 256 {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
	}
	if len(filter.After) > 2048 {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if value := values.Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100 {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
		filter.Limit = n
	}
	for key, target := range map[string]**time.Time{"created_after": &filter.CreatedAfter, "created_before": &filter.CreatedBefore} {
		if value := values.Get(key); value != "" {
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				writeStoreError(w, r, store.ErrInvalidInput)
				return
			}
			*target = &parsed
		}
	}
	if filter.CreatedAfter != nil && filter.CreatedBefore != nil && !filter.CreatedAfter.Before(*filter.CreatedBefore) {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	page, err := h.writeAudit.ListWriteOperations(r.Context(), tenant, filter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if page.Data == nil {
		page.Data = []store.WriteOperation{}
	}
	writeJSON(w, http.StatusOK, page)
}
