// Package node transports the finite sandbox Provider contract. Core remains the
// sole lifecycle owner; this package never retries a mutation or schedules work.
package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const ProtocolVersion = 3
const MaxControlFrameBytes = 32 * 1024
const MaxFrameBytes = 72 * 1024 * 1024
const maxPending = 32
const maxRequestTimeoutMillis int64 = 120000

var ErrUnavailable = errors.New("sandbox node unavailable")
var ErrAuthentication = errors.New("sandbox node authentication rejected")

type Identity struct {
	SpecificationDigest  string `json:"specification_digest"`
	DeploymentGeneration uint64 `json:"deployment_generation"`
	NodeID               string `json:"node_id"`
	InstallationID       string `json:"installation_id"`
	Provider             string `json:"provider"`
	BackendFingerprint   string `json:"backend_fingerprint"`
	MaxActive            int    `json:"max_active"`
	MaxRetained          int    `json:"max_retained"`
}

type Health struct {
	Generations          []sandbox.GenerationStatus `json:"generations,omitempty"`
	Diagnostic           string                     `json:"diagnostic,omitempty"`
	ProviderReady        bool                       `json:"provider_ready"`
	ObservedAt           time.Time                  `json:"observed_at"`
	ActiveOperations     int                        `json:"active_operations"`
	CPUCount             *int64                     `json:"cpu_count,omitempty"`
	CPUUtilization       *float64                   `json:"cpu_utilization"`
	TotalMemoryBytes     *int64                     `json:"total_memory_bytes"`
	EffectiveCPUCores    *float64                   `json:"effective_cpu_cores"`
	AvailableMemoryBytes *int64                     `json:"available_memory_bytes,omitempty"`
	AvailableDiskBytes   *int64                     `json:"available_disk_bytes,omitempty"`
}

type EnrollmentRequest struct {
	SpecificationDigest  string `json:"specification_digest"`
	DeploymentGeneration uint64 `json:"deployment_generation"`
	NodeID               string `json:"node_id"`
	Credential           string `json:"credential"`
	Name                 string `json:"name"`
	Provider             string `json:"provider"`
	BackendFingerprint   string `json:"backend_fingerprint"`
	// The retained Core origin; Core refuses an enrollment whose address is not its public URL.
	CoreURL string `json:"core_url"`
}

type EnrollmentResponse struct {
	MaxActive            int    `json:"max_active"`
	MaxRetained          int    `json:"max_retained"`
	SpecificationDigest  string `json:"specification_digest"`
	DeploymentGeneration uint64 `json:"deployment_generation"`
	Connected            bool   `json:"connected,omitempty"`
	ProviderReady        bool   `json:"provider_ready,omitempty"`
	NodeID               string `json:"node_id"`
	InstallationID       string `json:"installation_id"`
	Provider             string `json:"provider"`
}

type request struct {
	DeploymentGeneration uint64 `json:"deployment_generation,omitempty"`
	ID                   string `json:"id"`
	Sequence             uint64 `json:"sequence"`
	ConnectionID         string `json:"connection_id"`
	OwnerEpoch           uint64 `json:"owner_epoch"`
	Operation            string `json:"operation"`
	TimeoutMillis        int64  `json:"timeout_ms"`
	// deadline is anchored to the receiving host and never crosses the wire.
	deadline    time.Time
	Reference   sandbox.Reference         `json:"reference"`
	Bootstrap   *sandbox.Bootstrap        `json:"bootstrap,omitempty"`
	Compute     *sandbox.Compute          `json:"compute,omitempty"`
	Generation  uint64                    `json:"generation,omitempty"`
	Command     *sandbox.Command          `json:"command,omitempty"`
	Suspend     *sandbox.SuspendRequest   `json:"suspend,omitempty"`
	Resume      *sandbox.ResumeRequest    `json:"resume,omitempty"`
	Snapshot    *sandbox.SnapshotIdentity `json:"snapshot,omitempty"`
	Observation *runtimeobs.Target        `json:"observation,omitempty"`
}

type response struct {
	ID           string                 `json:"id"`
	ConnectionID string                 `json:"connection_id"`
	ErrorCode    string                 `json:"error_code,omitempty"`
	Info         *sandbox.Info          `json:"info,omitempty"`
	Compute      *sandbox.Compute       `json:"compute,omitempty"`
	State        *sandbox.ComputeState  `json:"state,omitempty"`
	Command      *sandbox.CommandResult `json:"command,omitempty"`
	Sample       *runtimeobs.Sample     `json:"sample,omitempty"`
}

type frame struct {
	GenerationManagement bool                    `json:"generation_management,omitempty"`
	Deployment           *sandbox.NodeDeployment `json:"deployment,omitempty"`
	Control              *generationControl      `json:"control,omitempty"`
	Version              int                     `json:"version"`
	Type                 string                  `json:"type"`
	Identity             *Identity               `json:"identity,omitempty"`
	Health               *Health                 `json:"health,omitempty"`
	ConnectionID         string                  `json:"connection_id,omitempty"`
	OwnerEpoch           uint64                  `json:"owner_epoch,omitempty"`
	Request              *request                `json:"request,omitempty"`
	Response             *response               `json:"response,omitempty"`
}

func validID(s string) bool {
	v, e := uuid.Parse(s)
	return e == nil && v != uuid.Nil && v.String() == s
}
func validReference(r sandbox.Reference) bool {
	return validID(r.TenantID) && validID(r.EnvironmentID) && validID(r.AllocationID)
}
func readFrame(conn *websocket.Conn) (frame, error) {
	var f frame
	kind, data, err := conn.ReadMessage()
	if err != nil {
		return f, err
	}
	if kind != websocket.TextMessage || len(data) > MaxFrameBytes {
		return f, sandbox.ErrInvalid
	}
	return decodeFrame(data)
}

func decodeFrame(data []byte) (frame, error) {
	var f frame
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&f) != nil || d.Decode(new(any)) != io.EOF || f.Version != ProtocolVersion {
		return frame{}, sandbox.ErrInvalid
	}
	if validateGenerationJSON(data, f.Type) != nil {
		return frame{}, sandbox.ErrInvalid
	}
	if err := validateVersionFrame(f, len(data)); err != nil {
		return frame{}, err
	}
	return f, nil
}
func writeFrame(conn *websocket.Conn, f frame) error {
	if f.Version == 0 {
		f.Version = ProtocolVersion
	}
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	data, err := json.Marshal(f)
	if err != nil || len(data) > MaxFrameBytes || validateVersionFrame(f, len(data)) != nil {
		return sandbox.ErrInvalid
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}
func errorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, runtimeobs.ErrUnavailable):
		return "observation_unavailable"
	case errors.Is(err, runtimeobs.ErrNotRunning):
		return "runtime_not_running"
	case errors.Is(err, sandbox.ErrInvalid):
		return "invalid"
	case errors.Is(err, sandbox.ErrOwnership):
		return "ownership"
	case errors.Is(err, sandbox.ErrExists):
		return "exists"
	case errors.Is(err, sandbox.ErrNotFound):
		return "not_found"
	case errors.Is(err, sandbox.ErrCommandUnconfirmed):
		return "command_unconfirmed"
	default:
		return "unconfirmed"
	}
}
func responseError(code string) error {
	switch code {
	case "":
		return nil
	case "observation_unavailable":
		return runtimeobs.ErrUnavailable
	case "runtime_not_running":
		return runtimeobs.ErrNotRunning
	case "invalid":
		return sandbox.ErrInvalid
	case "ownership":
		return sandbox.ErrOwnership
	case "exists":
		return sandbox.ErrExists
	case "not_found":
		return sandbox.ErrNotFound
	case "command_unconfirmed":
		return sandbox.ErrCommandUnconfirmed
	default:
		return sandbox.ErrComputeUnconfirmed
	}
}
func uncertain(operation string, err error) error {
	if operation == "command" || operation == "command_compute" {
		return errors.Join(sandbox.ErrCommandUnconfirmed, err)
	}
	return errors.Join(sandbox.ErrComputeUnconfirmed, err)
}
func (q request) validate() error {
	if !validID(q.ID) || !validReference(q.Reference) || q.TimeoutMillis < 1 || q.TimeoutMillis > maxRequestTimeoutMillis {
		return sandbox.ErrInvalid
	}
	count := 0
	for _, ok := range []bool{q.Bootstrap != nil, q.Compute != nil, q.Command != nil, q.Suspend != nil, q.Resume != nil, q.Snapshot != nil, q.Observation != nil} {
		if ok {
			count++
		}
	}
	switch q.Operation {
	case "observe":
		if count == 1 && q.Observation != nil && validObservation(*q.Observation, q.Reference) {
			return nil
		}
	case "create":
		if count == 1 && q.Bootstrap != nil && q.Bootstrap.Reference == q.Reference {
			return nil
		}
	case "info", "renew", "kill", "initial":
		if count == 0 {
			return nil
		}
	case "new_compute":
		if count == 0 || count == 1 && q.Snapshot != nil {
			return nil
		}
	case "compute", "kill_compute", "resume_compute":
		if count == 1 && q.Compute != nil {
			return nil
		}
	case "command":
		if count == 1 && q.Command != nil && len(q.Command.Stdin) <= sandbox.MaxCommandInputBytes {
			return nil
		}
	case "command_compute":
		if count == 2 && q.Command != nil && q.Compute != nil && len(q.Command.Stdin) <= sandbox.MaxCommandInputBytes {
			return nil
		}
	case "suspend":
		if count == 1 && q.Suspend != nil && q.Suspend.Reference == q.Reference {
			return nil
		}
	case "resume":
		if count == 1 && q.Resume != nil && q.Resume.Reference == q.Reference {
			return nil
		}
	case "delete_snapshot":
		if count == 1 && q.Snapshot != nil {
			return nil
		}
	}
	return sandbox.ErrInvalid
}
func execute(ctx context.Context, p sandbox.SandboxProvider, q request) response {
	out := response{ID: q.ID, ConnectionID: q.ConnectionID}
	err := q.validate()
	if err != nil {
		out.ErrorCode = errorCode(err)
		return out
	}
	var info sandbox.Info
	var command sandbox.CommandResult
	switch q.Operation {
	case "observe":
		var sample runtimeobs.Sample
		sample, err = observeProvider(ctx, p, *q.Observation)
		out.Sample = &sample
	case "create":
		info, err = p.Create(ctx, *q.Bootstrap)
		out.Info = &info
	case "info":
		info, err = p.GetInfo(ctx, q.Reference)
		out.Info = &info
	case "renew":
		info, err = p.Renew(ctx, q.Reference)
		out.Info = &info
	case "kill":
		err = p.Kill(ctx, q.Reference)
	case "command":
		command, err = p.RunCommand(ctx, q.Reference, *q.Command)
		out.Command = &command
	default:
		cp, ok := p.(sandbox.CheckpointProvider)
		if !ok {
			err = sandbox.ErrInvalid
			break
		}
		var state sandbox.ComputeState
		var compute sandbox.Compute
		switch q.Operation {
		case "initial":
			compute, err = cp.Initial(ctx, q.Reference)
			out.Compute = &compute
		case "new_compute":
			compute, err = cp.NewCompute(ctx, q.Reference, q.Generation, q.Snapshot)
			out.Compute = &compute
		case "compute":
			state, err = cp.GetCompute(ctx, q.Reference, *q.Compute)
			out.State = &state
		case "suspend":
			state, err = cp.Suspend(ctx, *q.Suspend)
			out.State = &state
		case "resume":
			state, err = cp.Resume(ctx, *q.Resume)
			out.State = &state
		case "kill_compute":
			err = cp.KillCompute(ctx, q.Reference, *q.Compute)
		case "delete_snapshot":
			err = cp.DeleteSnapshot(ctx, q.Reference, *q.Snapshot)
		case "resume_compute":
			state, err = cp.ResumeCompute(ctx, q.Reference, *q.Compute)
			out.State = &state
		case "command_compute":
			command, err = cp.RunCommandCompute(ctx, q.Reference, *q.Compute, *q.Command)
			out.Command = &command
		default:
			err = sandbox.ErrInvalid
		}
	}
	out.ErrorCode = errorCode(err)
	if err != nil {
		out.Sample = nil
		if !creationSettled(out.Info, q.Reference) {
			out.Info = nil
		}
		out.State = nil
		out.Command = nil
		out.Compute = nil
	}
	return out
}

func requiresReady(q request) bool {
	return q.Operation == "create" || q.Operation == "resume" && q.Resume != nil && !q.Resume.ObserveOnly
}

// setTimeout consumes sender queue time without comparing clocks across hosts.
func (q *request) setTimeout(deadline time.Time) error {
	millis := time.Until(deadline).Milliseconds()
	if millis < 1 {
		return context.DeadlineExceeded
	}
	if millis > maxRequestTimeoutMillis {
		millis = maxRequestTimeoutMillis
	}
	q.TimeoutMillis = millis
	return q.validate()
}

// receive anchors the bounded remaining budget once, before node queue admission.
func (q *request) receive(now time.Time) error {
	if err := q.validate(); err != nil {
		return err
	}
	q.deadline = now.Add(time.Duration(q.TimeoutMillis) * time.Millisecond)
	return nil
}
