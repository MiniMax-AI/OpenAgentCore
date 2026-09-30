package deployment

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
)

// resetTx serves stored and records each reset write, with its arguments.
func resetTx(t *testing.T, stored Record, resources Resources, source adminaudit.Source, calls *[]string) *fakeDeploymentTx {
	record := func(format string, args ...any) error {
		*calls = append(*calls, fmt.Sprintf(format, args...))
		return nil
	}
	return &fakeDeploymentTx{t: t,
		loadDeployment:  func() (Record, error) { return stored, nil },
		countResources:  func() (Resources, error) { return resources, nil },
		loadResetSource: func() (adminaudit.Source, error) { return source, nil },
		startReset: func(clear string, deadline int32, source adminaudit.Source) error {
			return record("StartReset %s %d %s", clear, deadline, source.CredentialID)
		},
		forceReset:    func() error { return record("ForceReset") },
		cancelReset:   func() error { return record("CancelReset") },
		completeReset: func() error { return record("CompleteReset") },
		recordAudit:   func(action, installation string) error { return record("RecordAudit %s %s", action, installation) },
		recordAuditAs: func(source adminaudit.Source, action, installation string) error {
			return record("RecordAuditAs %s %s %s", source.CredentialID, action, installation)
		},
	}
}

func resetting(t *testing.T, installation, clear string, requestedAt time.Time, deadline *time.Time) Record {
	d := webDeployment(t, installation, "docker", 4)
	d.Reset = &ResetState{Clear: clear, RequestedAt: requestedAt, DeadlineAt: deadline}
	return d
}

func TestStartResetDecisions(t *testing.T) {
	installation := uuid.NewString()
	admin := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "admin"})
	idle := webDeployment(t, installation, "docker", 4)
	unconfigured := idle
	unconfigured.Provider = ""
	seconds := func(v int32) *int32 { return &v }
	for name, test := range map[string]struct {
		ctx    context.Context
		stored Record
		input  ResetRequest
		want   error
		calls  []string
	}{
		"unknown clear":             {admin, idle, ResetRequest{ExpectedGeneration: 4, Clear: "sandboxes"}, ErrInvalidInput, nil},
		"deadline with force":       {admin, idle, ResetRequest{ExpectedGeneration: 4, Clear: ResetForce, DeadlineSeconds: seconds(600)}, ErrInvalidInput, nil},
		"deadline below minimum":    {admin, idle, ResetRequest{ExpectedGeneration: 4, Clear: ResetAuto, DeadlineSeconds: seconds(299)}, ErrInvalidInput, nil},
		"deadline above maximum":    {admin, idle, ResetRequest{ExpectedGeneration: 4, Clear: ResetAuto, DeadlineSeconds: seconds(86401)}, ErrInvalidInput, nil},
		"stale generation":          {admin, idle, ResetRequest{ExpectedGeneration: 3, Clear: ResetAuto}, ErrConflict, nil},
		"not configured":            {admin, unconfigured, ResetRequest{ExpectedGeneration: 4, Clear: ResetAuto}, ErrNotConfigured, nil},
		"no administrator source":   {t.Context(), idle, ResetRequest{ExpectedGeneration: 4, Clear: ResetAuto}, ErrInvalidInput, nil},
		"auto with default":         {admin, idle, ResetRequest{ExpectedGeneration: 4, Clear: ResetAuto}, nil, []string{"StartReset auto 3600 admin", "RecordAudit reset_start " + installation}},
		"auto with deadline":        {admin, idle, ResetRequest{ExpectedGeneration: 4, Clear: ResetAuto, DeadlineSeconds: seconds(86400)}, nil, []string{"StartReset auto 86400 admin", "RecordAudit reset_start " + installation}},
		"force":                     {admin, idle, ResetRequest{ExpectedGeneration: 4, Clear: ResetForce}, nil, []string{"StartReset force 3600 admin", "RecordAudit reset_start " + installation}},
		"same mode is a no-op":      {admin, resetting(t, installation, ResetAuto, time.Now(), nil), ResetRequest{ExpectedGeneration: 4, Clear: ResetAuto}, nil, nil},
		"force escalates auto":      {admin, resetting(t, installation, ResetAuto, time.Now(), nil), ResetRequest{ExpectedGeneration: 4, Clear: ResetForce}, nil, []string{"ForceReset", "RecordAudit reset_force " + installation}},
		"auto never replaces force": {admin, resetting(t, installation, ResetForce, time.Now(), nil), ResetRequest{ExpectedGeneration: 4, Clear: ResetAuto}, ErrResetInProgress, nil},
	} {
		var calls []string
		err := operations(t, testPublicURL, resetTx(t, test.stored, Resources{}, adminaudit.Source{}, &calls)).StartReset(test.ctx, installation, test.input)
		if !errors.Is(err, test.want) || (test.want == nil && err != nil) || !slices.Equal(calls, test.calls) {
			t.Errorf("%s: StartReset = %v with %q, want %v with %q", name, err, calls, test.want, test.calls)
		}
	}
}

func TestCancelResetDecisions(t *testing.T) {
	installation := uuid.NewString()
	for name, test := range map[string]struct {
		stored     Record
		generation uint64
		want       error
		calls      []string
	}{
		"stale generation":  {resetting(t, installation, ResetAuto, time.Now(), nil), 3, ErrConflict, nil},
		"no running reset":  {webDeployment(t, installation, "docker", 4), 4, nil, nil},
		"cancels the reset": {resetting(t, installation, ResetAuto, time.Now(), nil), 4, nil, []string{"CancelReset", "RecordAudit reset_cancel " + installation}},
	} {
		var calls []string
		err := operations(t, testPublicURL, resetTx(t, test.stored, Resources{}, adminaudit.Source{}, &calls)).CancelReset(t.Context(), installation, test.generation)
		if !errors.Is(err, test.want) || (test.want == nil && err != nil) || !slices.Equal(calls, test.calls) {
			t.Errorf("%s: CancelReset = %v with %q, want %v with %q", name, err, calls, test.want, test.calls)
		}
	}
}

// A passed auto deadline escalates to force under the source that started the
// reset, never the caller's.
func TestAdvanceResetDeadlineUsesTheStoredSource(t *testing.T) {
	installation := uuid.NewString()
	past, future := time.Now().Add(-time.Second), time.Now().Add(time.Hour)
	starter := adminaudit.Source{CredentialID: "starter"}
	for name, test := range map[string]struct {
		stored Record
		calls  []string
	}{
		"no reset":       {webDeployment(t, installation, "docker", 4), nil},
		"deadline ahead": {resetting(t, installation, ResetAuto, time.Now(), &future), nil},
		"already force":  {resetting(t, installation, ResetForce, time.Now(), nil), nil},
		"deadline passed": {resetting(t, installation, ResetAuto, time.Now(), &past),
			[]string{"ForceReset", "RecordAuditAs starter reset_deadline " + installation}},
	} {
		var calls []string
		if err := operations(t, testPublicURL, resetTx(t, test.stored, Resources{}, starter, &calls)).AdvanceResetDeadline(t.Context()); err != nil || !slices.Equal(calls, test.calls) {
			t.Errorf("%s: AdvanceResetDeadline = %v with %q, want %q", name, err, calls, test.calls)
		}
	}
}

func TestCompleteResetDecisions(t *testing.T) {
	installation := uuid.NewString()
	requestedAt := time.Now()
	starter := adminaudit.Source{CredentialID: "starter"}
	running := resetting(t, installation, ResetForce, requestedAt, nil)
	exhausted := running
	exhausted.OwnerEpoch = math.MaxInt64
	for name, test := range map[string]struct {
		stored      Record
		generation  uint64
		requestedAt time.Time
		resources   Resources
	}{
		"stale generation":          {running, 3, requestedAt, Resources{}},
		"no running reset":          {webDeployment(t, installation, "docker", 4), 4, requestedAt, Resources{}},
		"another reset":             {running, 4, requestedAt.Add(time.Second), Resources{}},
		"owner epoch exhausted":     {exhausted, 4, requestedAt, Resources{}},
		"resources still allocated": {running, 4, requestedAt, Resources{Allocations: 2, Pending: 1}},
	} {
		var calls []string
		if _, err := operations(t, testPublicURL, resetTx(t, test.stored, test.resources, starter, &calls)).CompleteReset(t.Context(), installation, test.generation, test.requestedAt); !errors.Is(err, ErrConflict) || len(calls) != 0 {
			t.Errorf("%s: CompleteReset = %v with %q, want a conflict and no writes", name, err, calls)
		}
	}

	var calls []string
	var inUse *InUseError
	_, err := operations(t, testPublicURL, resetTx(t, running, Resources{Allocations: 2, Pending: 1}, starter, &calls)).CompleteReset(t.Context(), installation, 4, requestedAt)
	if !errors.As(err, &inUse) || inUse.Resources != (Resources{Allocations: 2, Pending: 1}) {
		t.Fatalf("CompleteReset with resources = %v", err)
	}

	committed, err := operations(t, testPublicURL, resetTx(t, running, Resources{}, starter, &calls)).CompleteReset(t.Context(), installation, 4, requestedAt)
	if want := []string{"CompleteReset", "RecordAuditAs starter reset_complete " + installation}; err != nil || committed != 5 || !slices.Equal(calls, want) {
		t.Fatalf("CompleteReset = %d, %v with %q, want 5 with %q", committed, err, calls, want)
	}
}
