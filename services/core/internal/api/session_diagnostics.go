package api

import (
	"context"
	"net/http"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
)

type DiagnosticFailure struct {
	Code     string           `json:"code" enums:"harness_error,model_provider_required,runtime_unavailable,runtime_disconnected,runtime_preparation_failed,execution_interrupted,delivery_unconfirmed,input_rejected,executor_protocol_error,core_storage_failed,internal_error,environment_connection_timeout,environment_unavailable,environment_provisioning_failed,authentication_error,rate_limit_exceeded,usage_limit_exceeded,server_overloaded,server_error,invalid_request,resource_not_found,request_timeout,context_length_exceeded,cyber_policy,connection_failed"`
	Params   CoreErrorDetails `json:"params" swaggertype:"object"`
	FailedAt *time.Time       `json:"failed_at" extensions:"x-nullable"`
}

type SessionDiagnosticFailure struct {
	DiagnosticFailure
	Source string `json:"source" enums:"turn,environment,environment_input"`
	TurnID string `json:"turn_id,omitempty"`
}

type SessionDiagnostics struct {
	Object    string                    `json:"object" enums:"core.session_diagnostics"`
	SessionID string                    `json:"session_id"`
	Status    string                    `json:"status" enums:"idle,in_progress,requires_action,failed"`
	Failure   *SessionDiagnosticFailure `json:"failure" extensions:"x-nullable"`
}

type ItemDiagnosticTiming struct {
	ItemID             string     `json:"item_id"`
	StartedAt          time.Time  `json:"started_at"`
	CompletedAt        *time.Time `json:"completed_at" extensions:"x-nullable"`
	ObservedDurationMS *int64     `json:"observed_duration_ms" extensions:"x-nullable"`
}

type TurnDiagnostics struct {
	Object         string                 `json:"object" enums:"core.turn_diagnostics"`
	SessionID      string                 `json:"session_id"`
	TurnID         string                 `json:"turn_id"`
	Status         string                 `json:"status"`
	Failure        *DiagnosticFailure     `json:"failure" extensions:"x-nullable"`
	Items          []ItemDiagnosticTiming `json:"items"`
	ItemsTruncated bool                   `json:"items_truncated"`
}

// SessionAdmin serves the administrator's per-Session reads: diagnostics
// snapshots, the execution configuration and the managed archive state.
type SessionAdmin interface {
	GetSessionDiagnosticsSnapshot(context.Context, string, string) (store.Session, error)
	GetTurnDiagnosticsSnapshot(context.Context, string, string, string) (store.TurnDiagnosticsSnapshot, error)
	GetSessionExecutionConfiguration(context.Context, string, string) (v1.SessionExecutionConfiguration, error)
	GetManagedSessionArchive(context.Context, string, string) (store.ManagedSessionArchive, error)
}

// @Summary Retrieve root Session diagnostics
// @Description Core key only. Safe failure categories from one committed snapshot; no native text or historical inference.
// @Tags Sessions
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project ID"
// @Param session_id path string true "Session ID"
// @Success 200 {object} SessionDiagnostics
// @Failure 400,401,404,500,503 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/diagnostics [get]
func (h *Handler) getSessionDiagnostics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	session, err := h.SessionAdmin.GetSessionDiagnosticsSnapshot(ctx, tenantID(r), chi.URLParam(r, "session_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	public, err := sessionResponse(session, h.executorURL())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := SessionDiagnostics{Object: "core.session_diagnostics", SessionID: public.ID, Status: public.Status}
	if public.Status == "failed" {
		failure := SessionDiagnosticFailure{DiagnosticFailure: DiagnosticFailure{Code: "internal_error", Params: CoreErrorDetails{}}}
		switch {
		case session.EnvironmentFailure != nil:
			failure.Source = "environment"
			failure.Code = "environment_provisioning_failed"
			failure.FailedAt = diagnosticTime(session.EnvironmentFailure.FailedAt)
			failure.Params = provisioningFailureParams(session.EnvironmentFailure.Detail)
		case session.EnvironmentInputActivity != nil:
			failure.Source = "environment_input"
			failure.FailedAt = diagnosticTime(session.EnvironmentInputActivity.LastActiveAt)
			switch session.EnvironmentInputActivity.Failure {
			case "":
				failure.Code = "environment_connection_timeout"
			case "environment_unavailable":
				failure.Code = "environment_unavailable"
			case "runtime_preparation_failed":
				failure.Code = "runtime_preparation_failed"
			case "model_provider_required":
				failure.Code = "model_provider_required"
			}
		case session.LastTurn != nil:
			failure.Source, failure.TurnID = "turn", session.LastTurn.ID
			failure.DiagnosticFailure = *turnDiagnosticFailure(*session.LastTurn)
		}
		response.Failure = &failure
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

// @Summary Retrieve root Turn diagnostics
// @Description Core key only. At most 1000 root Item receipt timings in public Item order. Receipt intervals are not native execution durations.
// @Tags Turns
// @Produce json
// @Security DeploymentAdminAuth
// @Param project_id path string true "Project ID"
// @Param session_id path string true "Session ID"
// @Param turn_id path string true "Root Turn ID"
// @Success 200 {object} TurnDiagnostics
// @Failure 400,401,404,500,503 {object} CoreErrorResponse
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/turns/{turn_id}/diagnostics [get]
func (h *Handler) getTurnDiagnostics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	snapshot, err := h.SessionAdmin.GetTurnDiagnosticsSnapshot(ctx, tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "turn_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	public, err := turnResponse(snapshot.Session, snapshot.Turn)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := TurnDiagnostics{Object: "core.turn_diagnostics", SessionID: public.SessionID, TurnID: public.ID, Status: public.Status, Failure: turnDiagnosticFailure(snapshot.Turn), Items: []ItemDiagnosticTiming{}, ItemsTruncated: snapshot.ItemsTruncated}
	for _, value := range snapshot.Items {
		item := ItemDiagnosticTiming{ItemID: value.ItemID, StartedAt: value.StartedAt, CompletedAt: value.CompletedAt}
		if value.CompletedAt != nil {
			duration := value.CompletedAt.Sub(value.StartedAt).Milliseconds()
			item.ObservedDurationMS = &duration
		}
		response.Items = append(response.Items, item)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func diagnosticTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func provisioningFailureParams(detail *sessions.ProvisioningFailureDetail) CoreErrorDetails {
	params := CoreErrorDetails{"step": CoreErrorNull(), "index": CoreErrorNull(), "exit_code": CoreErrorNull()}
	if detail == nil {
		return params
	}
	if detail.Step != nil {
		params["step"] = CoreErrorString(*detail.Step)
	}
	if detail.Index != nil {
		params["index"] = CoreErrorNumber(float64(*detail.Index))
	}
	if detail.ExitCode != nil {
		params["exit_code"] = CoreErrorNumber(float64(*detail.ExitCode))
	}
	return params
}
