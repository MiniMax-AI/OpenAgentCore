package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type AdminSessionCounts struct {
	Total          int64 `json:"total"`
	Idle           int64 `json:"idle"`
	InProgress     int64 `json:"in_progress"`
	RequiresAction int64 `json:"requires_action"`
	Failed         int64 `json:"failed"`
}
type AdminUsageCoverage struct {
	MeasuredSessions int64    `json:"measured_sessions"`
	TotalSessions    int64    `json:"total_sessions"`
	Ratio            *float64 `json:"ratio"`
}
type AdminSummaryRow struct {
	ProjectID    string                  `json:"project_id"`
	KeyID        *string                 `json:"key_id"`
	AgentID      *string                 `json:"agent_id"`
	Assets       *store.AdminAssetCounts `json:"assets"`
	Sessions     AdminSessionCounts      `json:"sessions"`
	Usage        v1.TokenUsage           `json:"usage"`
	Coverage     AdminUsageCoverage      `json:"coverage"`
	LastActiveAt *int64                  `json:"last_active_at"`
}
type AdminSummaryResponse struct {
	Data       []AdminSummaryRow `json:"data"`
	HasMore    bool              `json:"has_more"`
	NextCursor string            `json:"next_cursor"`
}

func adminSummaryTime(r *http.Request, name string) (*time.Time, error) {
	values, ok := r.URL.Query()[name]
	if !ok {
		return nil, nil
	}
	if len(values) != 1 || values[0] == "" {
		return nil, store.ErrInvalidInput
	}
	value, err := time.Parse(time.RFC3339Nano, values[0])
	if err != nil {
		return nil, store.ErrInvalidInput
	}
	return &value, nil
}

// @Summary Summarize resource counts and Session usage by Project, Agent or creator key
// @Description Administrator only. after/limit/order paginate Projects. Agent grouping returns groups within those spaces. Date bounds filter Session creation, not current asset counts. Usage sums only non-null public Session usage; coverage includes every selected Session. Each Project is read in a consistent database snapshot. Totals are not billing records.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id query string false "One API Project"
// @Param group_by query string false "Grouping" Enums(project,agent,key) default(project)
// @Param created_after query string false "Inclusive RFC3339 Session creation time"
// @Param created_before query string false "Exclusive RFC3339 Session creation time"
// @Param after query string false "Last Project ID in preceding page"
// @Param limit query int false "Number of Projects" minimum(1) maximum(100) default(20)
// @Param order query string false "Project order" Enums(asc,desc) default(desc)
// @Success 200 {object} api.AdminSummaryResponse
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/admin/summary [get]
func (h *Handler) adminSummary(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r, "project_id", "group_by", "created_after", "created_before")
	if !ok {
		return
	}
	group := r.URL.Query().Get("group_by")
	if group == "" {
		group = "project"
	}
	if group != "project" && group != "agent" && group != "key" {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	after, err := adminSummaryTime(r, "created_after")
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	before, err := adminSummaryTime(r, "created_before")
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if after != nil && before != nil && !after.Before(*before) {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if group == "agent" && r.URL.Query().Get("project_id") == "" {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	var projects store.ProjectPage
	if projectID := r.URL.Query().Get("project_id"); projectID != "" {
		if options.after != "" {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
		var binding store.ProjectBinding
		binding, err = h.resolveAdminProject(ctx, projectID)
		projects = store.ProjectPage{Data: []store.Project{binding.Project}}
	} else {
		projects, err = h.listAdminProjects(ctx, options.after, options.limit, options.ascending)
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := AdminSummaryResponse{Data: []AdminSummaryRow{}, HasMore: projects.HasMore}
	for _, project := range projects.Data {
		groups := map[string]*AdminSummaryRow{}
		if group == "project" {
			groups[""] = &AdminSummaryRow{ProjectID: project.ID}
		}
		counts, err := h.adminManagement.ReadAdminSummary(ctx, project.TenantID, store.AdminSummaryFilter{CreatedAfter: after, CreatedBefore: before}, func(session store.Session, creationKeyID *string) error {
			projected, err := sessionResponse(session, h.executorURL)
			if err != nil {
				return err
			}
			groupID := ""
			if group == "agent" {
				groupID = projected.Agent.ID
			} else if group == "key" && creationKeyID != nil {
				groupID = *creationKeyID
			}
			row := groups[groupID]
			if row == nil {
				row = &AdminSummaryRow{ProjectID: project.ID}
				if group == "agent" {
					id := groupID
					row.AgentID = &id
				} else if group == "key" {
					row.KeyID = creationKeyID
				}
				groups[groupID] = row
			}
			row.Sessions.Total++
			switch projected.Status {
			case "idle":
				row.Sessions.Idle++
			case "in_progress":
				row.Sessions.InProgress++
			case "requires_action":
				row.Sessions.RequiresAction++
			case "failed":
				row.Sessions.Failed++
			}
			if row.LastActiveAt == nil || projected.LastActiveAt > *row.LastActiveAt {
				at := projected.LastActiveAt
				row.LastActiveAt = &at
			}
			row.Coverage.TotalSessions++
			if usage := projected.Usage; usage != nil {
				row.Coverage.MeasuredSessions++
				row.Usage.InputTokens += usage.InputTokens
				row.Usage.OutputTokens += usage.OutputTokens
				row.Usage.TotalTokens += usage.TotalTokens
				row.Usage.InputTokensDetails.CachedTokens += usage.InputTokensDetails.CachedTokens
				row.Usage.OutputTokensDetails.ReasoningTokens += usage.OutputTokensDetails.ReasoningTokens
			}
			return nil
		})
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		if group == "project" {
			groups[""].Assets = &counts
		}
		ids := make([]string, 0, len(groups))
		for id := range groups {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			row := groups[id]
			if row.Coverage.TotalSessions > 0 {
				ratio := float64(row.Coverage.MeasuredSessions) / float64(row.Coverage.TotalSessions)
				row.Coverage.Ratio = &ratio
			}
			response.Data = append(response.Data, *row)
		}
	}
	if projects.HasMore && len(projects.Data) > 0 {
		response.NextCursor = projects.Data[len(projects.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}
