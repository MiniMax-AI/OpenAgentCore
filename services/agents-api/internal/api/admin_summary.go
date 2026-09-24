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
	KeyID        string                  `json:"key_id"`
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

// @Summary Summarize resource counts and Session usage by key or Agent
// @Description Administrator only. after/limit/order paginate key spaces. Agent grouping returns groups within those spaces. Date bounds filter Session creation, not current asset counts. Usage sums only non-null public Session usage; coverage includes every selected Session. Each key is read in a consistent database snapshot. Totals are not billing records.
// @Tags Core Administration
// @Produce json
// @Security DeploymentAdminAuth
// @Param key_id query string false "One API key space"
// @Param group_by query string false "Grouping" Enums(key,agent) default(key)
// @Param created_after query string false "Inclusive RFC3339 Session creation time"
// @Param created_before query string false "Exclusive RFC3339 Session creation time"
// @Param after query string false "Last key ID in preceding page"
// @Param limit query int false "Number of key spaces" minimum(1) maximum(100) default(20)
// @Param order query string false "Key ID order" Enums(asc,desc) default(desc)
// @Success 200 {object} api.AdminSummaryResponse
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /core/v1/admin/summary [get]
func (h *Handler) adminSummary(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r, "key_id", "group_by", "created_after", "created_before")
	if !ok {
		return
	}
	group := r.URL.Query().Get("group_by")
	if group == "" {
		group = "key"
	}
	if group != "key" && group != "agent" {
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
	var keys store.ProjectAPIKeyPage
	if keyID := r.URL.Query().Get("key_id"); keyID != "" {
		if options.after != "" {
			writeStoreError(w, r, store.ErrInvalidInput)
			return
		}
		var binding store.ProjectAPIKeyBinding
		binding, err = h.resolveAdminKey(ctx, keyID)
		keys = store.ProjectAPIKeyPage{Data: []store.ProjectAPIKey{binding.Key}}
	} else {
		keys, err = h.listAdminKeyBindings(ctx, options.after, options.limit, options.ascending)
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := AdminSummaryResponse{Data: []AdminSummaryRow{}, HasMore: keys.HasMore}
	for _, key := range keys.Data {
		groups := map[string]*AdminSummaryRow{}
		if group == "key" {
			groups[""] = &AdminSummaryRow{KeyID: key.ID}
		}
		counts, err := h.adminManagement.ReadAdminSummary(ctx, key.TenantID, store.AdminSummaryFilter{CreatedAfter: after, CreatedBefore: before}, func(session store.Session) error {
			projected, err := sessionResponse(session, h.executorURL)
			if err != nil {
				return err
			}
			groupID := ""
			if group == "agent" {
				groupID = projected.Agent.ID
			}
			row := groups[groupID]
			if row == nil {
				id := groupID
				row = &AdminSummaryRow{KeyID: key.ID, AgentID: &id}
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
		if group == "key" {
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
	if keys.HasMore && len(keys.Data) > 0 {
		response.NextCursor = keys.Data[len(keys.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}
