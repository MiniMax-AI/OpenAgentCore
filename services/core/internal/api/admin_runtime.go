package api

import (
	"context"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
)

type AdminRuntimeObservation struct {
	ProjectID   string                        `json:"project_id"`
	Observation AdminRuntimeObservationDetail `json:"observation"`
}

// AdminRuntimeObservationDetail adds administrator-only measurements to the
// project observation shape, which stays unchanged.
type AdminRuntimeObservationDetail struct {
	v1.RuntimeObservation
	// Current Runtime disk usage and capacity. E2B reports them; Docker and
	// microsandbox observations return null.
	Disk *RuntimeDiskObservation `json:"disk" extensions:"x-nullable" binding:"required"`
}

// RuntimeDiskObservation has the memory observation's shape and null rules.
type RuntimeDiskObservation struct {
	UsageBytes *uint64 `json:"usage_bytes" extensions:"x-nullable" binding:"required" minimum:"0"`
	LimitBytes *uint64 `json:"limit_bytes" extensions:"x-nullable" binding:"required" minimum:"1"`
}
type AdminRuntimeObservationList struct {
	Object  string                    `json:"object"`
	Data    []AdminRuntimeObservation `json:"data"`
	HasMore bool                      `json:"has_more"`
	FirstID *string                   `json:"first_id"`
	LastID  *string                   `json:"last_id"`
}

// @Summary List Runtime observations across managed Projects
// @Description Core key only. Each observation is labelled with its owning Project ID. Uses the existing read-only Runtime sampler, with bounded concurrency and no execution or provisioning. A provider with a batch metrics read, such as E2B, samples the page's running sandboxes in one bounded request.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Param after query string false "Last Session ID from the preceding page"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Session creation order" Enums(asc,desc) default(desc)
// @Success 200 {object} api.AdminRuntimeObservationList
// @Failure 400,401,404,500,503 {object} CoreErrorResponse
// @Router /core/v1/sandbox/runtime-observations [get]
func (h *Handler) adminRuntimeObservations(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), runtimeObservationRequestBudget)
	defer cancel()
	tenants := []string{}
	projectByTenant := map[string]string{}
	cursor := ""
	for {
		projects, err := h.Projects.ListProjects(ctx, cursor, 100, true)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		for _, project := range projects.Data {
			tenants = append(tenants, project.TenantID)
			projectByTenant[project.TenantID] = project.ID
			cursor = project.ID
		}
		if !projects.HasMore {
			break
		}
	}
	page, err := h.Admin.ListAdminRuntimeTargets(ctx, tenants, options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	sessions := make([]runtimeobs.SessionIdentity, len(page.Data))
	for index, target := range page.Data {
		sessions[index] = runtimeobs.SessionIdentity{TenantID: target.TenantID, SessionID: target.SessionID}
	}
	observations, errs := h.RuntimeObservations.ObserveSessions(ctx, sessions, runtimeObservationPage)
	if ctx.Err() != nil {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Runtime observation collection exceeded its request budget.")
		return
	}
	if err := firstRuntimeObservationError(errs); err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := AdminRuntimeObservationList{Object: "list", Data: make([]AdminRuntimeObservation, len(page.Data)), HasMore: page.HasMore}
	for index, observation := range observations {
		detail, err := adminRuntimeObservationResponse(observation)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		response.Data[index] = AdminRuntimeObservation{ProjectID: projectByTenant[page.Data[index].TenantID], Observation: detail}
	}
	if len(response.Data) > 0 {
		first, last := page.Data[0].SessionID, page.Data[len(page.Data)-1].SessionID
		response.FirstID = &first
		response.LastID = &last
	}
	writeJSON(w, http.StatusOK, response)
}

func adminRuntimeObservationResponse(observation runtimeobs.Observation) (AdminRuntimeObservationDetail, error) {
	projected, err := runtimeObservationResponse(observation)
	if err != nil {
		return AdminRuntimeObservationDetail{}, err
	}
	detail := AdminRuntimeObservationDetail{RuntimeObservation: projected}
	if sample := observation.Sample; sample != nil && (sample.DiskUsageBytes != nil || sample.DiskLimitBytes != nil) {
		detail.Disk = &RuntimeDiskObservation{UsageBytes: sample.DiskUsageBytes, LimitBytes: sample.DiskLimitBytes}
	}
	return detail, nil
}
