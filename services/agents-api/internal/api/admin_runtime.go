package api

import (
	"context"
	"net/http"
	"sync"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

type AdminRuntimeObservation struct {
	ProjectID   string                `json:"project_id"`
	Observation v1.RuntimeObservation `json:"observation"`
}
type AdminRuntimeObservationList struct {
	Object  string                    `json:"object"`
	Data    []AdminRuntimeObservation `json:"data"`
	HasMore bool                      `json:"has_more"`
	FirstID *string                   `json:"first_id"`
	LastID  *string                   `json:"last_id"`
}

// @Summary List Runtime observations across managed Projects
// @Description Deployment administrator only. Each observation is labelled with its owning Project ID. Uses the existing read-only Runtime sampler, with bounded concurrency and no execution or provisioning.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Param after query string false "Last Session ID from the preceding page"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Session creation order" Enums(asc,desc) default(desc)
// @Success 200 {object} api.AdminRuntimeObservationList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /core/v1/admin/runtime-observations [get]
func (h *Handler) adminRuntimeObservations(w http.ResponseWriter, r *http.Request) {
	if h.runtimeObservations == nil {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Runtime observation is not configured on this service.")
		return
	}
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
		projects, err := h.listAdminProjects(ctx, cursor, 100, true)
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
	page, err := h.adminManagement.ListAdminRuntimeTargets(ctx, tenants, options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := AdminRuntimeObservationList{Object: "list", Data: make([]AdminRuntimeObservation, len(page.Data)), HasMore: page.HasMore}
	work, stop := context.WithCancel(ctx)
	defer stop()
	semaphore := make(chan struct{}, runtimeObservationConcurrency)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	for i, target := range page.Data {
		wg.Add(1)
		go func(i int, tenant, id string) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-work.Done():
				return
			}
			sampleCtx, sampleCancel := context.WithTimeout(work, runtimeObservationSourceBudget)
			defer sampleCancel()
			observed, err := h.runtimeObservations.ObserveSession(sampleCtx, tenant, id)
			var projected v1.RuntimeObservation
			if err == nil {
				projected, err = runtimeObservationResponse(observed)
			}
			if err != nil {
				once.Do(func() { firstErr = err; stop() })
				return
			}
			response.Data[i] = AdminRuntimeObservation{ProjectID: projectByTenant[tenant], Observation: projected}
		}(i, target.TenantID, target.SessionID)
	}
	wg.Wait()
	if firstErr != nil {
		writeStoreError(w, r, firstErr)
		return
	}
	if ctx.Err() != nil {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Runtime observation collection exceeded its request budget.")
		return
	}
	if len(response.Data) > 0 {
		first, last := page.Data[0].SessionID, page.Data[len(page.Data)-1].SessionID
		response.FirstID = &first
		response.LastID = &last
	}
	writeJSON(w, http.StatusOK, response)
}
