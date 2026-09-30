package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/go-chi/chi/v5"
)

const (
	runtimeObservationConcurrency   = 8
	runtimeObservationSourceBudget  = 2 * time.Second
	runtimeObservationRequestBudget = 10 * time.Second
)

var runtimeProviderTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// RuntimeObservations samples the current Runtime of one or more Sessions.
type RuntimeObservations interface {
	ObserveSession(context.Context, string, string) (runtimeobs.Observation, error)
	ObserveSessions(context.Context, []runtimeobs.SessionIdentity, runtimeobs.PageOptions) ([]runtimeobs.Observation, []error)
}

// runtimeObservationPage bounds the administrator list fan-out.
var runtimeObservationPage = runtimeobs.PageOptions{Concurrency: runtimeObservationConcurrency, SourceTimeout: runtimeObservationSourceBudget}

func firstRuntimeObservationError(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// getRuntimeObservation serves the administrator per-Session observation read.
func (h *Handler) getRuntimeObservation(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Runtime observation retrieval does not accept query parameters.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), runtimeObservationSourceBudget)
	defer cancel()
	observation, err := h.RuntimeObservations.ObserveSession(ctx, tenantID(r), chi.URLParam(r, "session_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response, err := runtimeObservationResponse(observation)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func runtimeObservationResponse(observation runtimeobs.Observation) (v1.RuntimeObservation, error) {
	if observation.Target.SessionID == "" || observation.ResolvedAt.IsZero() || observation.ResolvedAt.Unix() < 0 {
		return v1.RuntimeObservation{}, errors.New("invalid Runtime observation identity")
	}
	if !observation.Target.Instance.AllocationCreatedAt.IsZero() &&
		(observation.Target.Instance.AllocationCreatedAt.Unix() < 0 || observation.Target.Instance.AllocationCreatedAt.After(observation.ResolvedAt)) {
		return v1.RuntimeObservation{}, errors.New("invalid Runtime allocation creation time")
	}
	if observation.Sample != nil {
		if observation.Sample.ObservedAt.IsZero() || observation.Sample.ObservedAt.Unix() < 0 || observation.Sample.ObservedAt.After(observation.ResolvedAt) {
			return v1.RuntimeObservation{}, errors.New("invalid Runtime sample time")
		}
		if observation.Sample.StartedAt != nil &&
			(observation.Sample.StartedAt.IsZero() || observation.Sample.StartedAt.Unix() < 0 || observation.Sample.StartedAt.After(observation.Sample.ObservedAt)) {
			return v1.RuntimeObservation{}, errors.New("invalid Runtime start time")
		}
	}
	result := v1.RuntimeObservation{
		ID: observation.Target.SessionID, Object: "agent.runtime_observation", SessionID: observation.Target.SessionID,
		Mode: string(observation.Target.Mode), Status: string(observation.Status), ResolvedAt: observation.ResolvedAt.Unix(),
	}
	if observation.Target.EnvironmentID != "" {
		result.EnvironmentID = &observation.Target.EnvironmentID
	}
	if observation.ProviderType != "" {
		if !runtimeProviderTypePattern.MatchString(observation.ProviderType) {
			return v1.RuntimeObservation{}, errors.New("invalid Runtime observation provider type")
		}
		result.ProviderType = &observation.ProviderType
	}
	if observation.Reason != "" {
		result.Reason = &observation.Reason
	}
	switch observation.Target.Mode {
	case runtimeobs.ModeManaged:
		result.Instance.Kind = "managed_allocation"
		lifecycleState, err := runtimeLifecycleState(observation.Target.Instance)
		if err != nil {
			return v1.RuntimeObservation{}, err
		}
		result.LifecycleState = &lifecycleState
		if observation.Target.Instance.AllocationID != "" {
			result.Instance.AllocationID = &observation.Target.Instance.AllocationID
		}
		if observation.Target.Instance.DeviceID != "" {
			result.Instance.DeviceID = &observation.Target.Instance.DeviceID
		}
		if !observation.Target.Instance.AllocationCreatedAt.IsZero() {
			created := observation.Target.Instance.AllocationCreatedAt.Unix()
			result.AllocationCreatedAt = &created
		}
	case runtimeobs.ModeSelfHosted:
		result.Instance.Kind = "self_hosted_connection"
		if observation.Target.Instance.DeviceID != "" {
			result.Instance.DeviceID = &observation.Target.Instance.DeviceID
		}
		if observation.Target.Instance.ConnectionGeneration != "" {
			result.Instance.ConnectionGeneration = &observation.Target.Instance.ConnectionGeneration
		}
	case runtimeobs.ModeNone:
		result.Instance.Kind = "none"
	default:
		return v1.RuntimeObservation{}, errors.New("invalid Runtime observation mode")
	}
	if observation.Sample == nil {
		return result, nil
	}
	observedAt := observation.Sample.ObservedAt.Unix()
	result.ObservedAt = &observedAt
	if observation.Sample.StartedAt != nil {
		startedAt := observation.Sample.StartedAt.Unix()
		result.StartedAt = &startedAt
	}
	if observation.Sample.CPUUsageSecondsTotal != nil || observation.Sample.CPUCapacityCores != nil || observation.Sample.CPUUtilizationRatio != nil {
		result.CPU = &v1.RuntimeCPUObservation{UsageSecondsTotal: observation.Sample.CPUUsageSecondsTotal, CapacityCores: observation.Sample.CPUCapacityCores, UtilizationRatio: observation.Sample.CPUUtilizationRatio}
	}
	if observation.Sample.MemoryUsageBytes != nil || observation.Sample.MemoryLimitBytes != nil {
		result.Memory = &v1.RuntimeMemoryObservation{UsageBytes: observation.Sample.MemoryUsageBytes, LimitBytes: observation.Sample.MemoryLimitBytes}
	}
	return result, nil
}

func runtimeLifecycleState(instance runtimeobs.Instance) (string, error) {
	switch instance.AllocationState {
	case "":
		if instance.AllocationID == "" {
			return "pending", nil
		}
		return "", errors.New("invalid Runtime allocation state")
	case "creating":
		return "pending", nil
	case "cleanup_pending", "released":
		return "stopped", nil
	case "running":
		switch instance.ComputePhase {
		case "suspended":
			return "sleeping", nil
		case "quiescing", "suspending", "restoring", "waking":
			return "transitioning", nil
		case "disabled", "running":
			return "active", nil
		default:
			return "", errors.New("invalid Runtime compute phase")
		}
	default:
		return "", errors.New("invalid Runtime allocation state")
	}
}
