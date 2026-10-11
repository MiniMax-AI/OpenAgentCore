package workspaces

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/google/uuid"
)

type memoryWorkspace struct {
	active         workspacefs.ConfigurationRecord
	retained       map[string]workspacefs.ConfigurationRecord
	tenantCapacity uint32
	record         *Record
	deletedSession bool
	allocation     bool
	expired        bool
	beforeBind     func()
}

func (s *memoryWorkspace) ActiveConfiguration(context.Context) (workspacefs.ConfigurationRecord, error) {
	if s.active.ID == "" {
		return s.active, ErrNotConfigured
	}
	return s.active, nil
}
func (s *memoryWorkspace) Configuration(_ context.Context, id string) (workspacefs.ConfigurationRecord, error) {
	if c, ok := s.retained[id]; ok {
		return c, nil
	}
	return workspacefs.ConfigurationRecord{}, ErrNotFound
}
func (s *memoryWorkspace) Selection(context.Context) (Selection, error) {
	return Selection{Configuration: s.active.Configuration, TenantCapacityMiB: s.tenantCapacity}, nil
}
func (s *memoryWorkspace) SelectConfiguration(_ context.Context, c workspacefs.ConfigurationRecord, tenantCapacity uint32) error {
	s.active, s.tenantCapacity = c, tenantCapacity
	return nil
}
func (s *memoryWorkspace) Get(context.Context, string, string) (Record, error) {
	if s.record == nil {
		return Record{}, ErrNotFound
	}
	return *s.record, nil
}
func (s *memoryWorkspace) Bind(_ context.Context, tenant, environment, configuration string, capacity uint32) (Record, error) {
	if s.beforeBind != nil {
		s.beforeBind()
	}
	if s.record != nil {
		if s.record.State == Deleting || s.record.State == Deleted {
			return Record{}, ErrConflict
		}
		return *s.record, nil
	}
	if s.active.ID == "" {
		return Record{}, ErrNotConfigured
	}
	if s.active.ID != configuration {
		return Record{}, ErrConflict
	}
	s.record = &Record{Reference: workspacefs.Reference{TenantID: tenant, EnvironmentID: environment, ObjectID: uuid.NewString()}, Configuration: s.active, CapacityMiB: capacity, State: Creating}
	return *s.record, nil
}
func (s *memoryWorkspace) MarkReady(_ context.Context, r workspacefs.Reference, a workspacefs.Attachment) error {
	if s.deletedSession {
		return ErrConflict
	}
	s.record.State = Ready
	s.record.Attachment = &a
	return nil
}
func (s *memoryWorkspace) BeginDelete(context.Context, workspacefs.Reference) error {
	if !s.deletedSession || s.allocation {
		return ErrConflict
	}
	s.record.State = Deleting
	return nil
}
func (s *memoryWorkspace) MarkDeleted(context.Context, workspacefs.Reference) error {
	s.record.State = Deleted
	return nil
}
func (s *memoryWorkspace) DeletionCandidates(_ context.Context, cursor string) ([]Record, error) {
	if s.record != nil && s.record.State != Deleted && (s.deletedSession || s.record.State == Deleting) && !s.allocation && s.record.Reference.ObjectID > cursor {
		return []Record{*s.record}, nil
	}
	return nil, nil
}

type workspaceControl struct {
	store                *memoryWorkspace
	quota                bool
	createErr, deleteErr error
	creates, deletes     int
	capacities           []uint32
	checks               int
	checkErr             error
	normalized           []json.RawMessage
}

func (c *workspaceControl) Control(config workspacefs.ConfigurationRecord) (workspacefs.Control, error) {
	if c.store.record != nil && config.ID != c.store.record.Configuration.ID {
		return nil, workspacefs.ErrOwnership
	}
	return c, nil
}
func (c *workspaceControl) Normalize(cn workspacefs.Configuration, credential json.RawMessage) (workspacefs.ConfigurationRecord, error) {
	c.normalized = append(c.normalized, credential)
	return workspacefs.ConfigurationRecord{Configuration: cn, Credential: credential}, nil
}
func (c *workspaceControl) Check(context.Context) error { c.checks++; return c.checkErr }
func (c *workspaceControl) Declaration() workspacefs.Declaration {
	return workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true, CapacityQuota: c.quota}
}
func (c *workspaceControl) Create(_ context.Context, r workspacefs.Reference, capacity uint32) (workspacefs.Attachment, error) {
	c.creates++
	c.capacities = append(c.capacities, capacity)
	if c.store.record == nil || c.store.record.Reference != r {
		panic("create before durable bind")
	}
	return workspacefs.Attachment{Reference: r, ConfigurationID: c.store.record.Configuration.ID, Kind: workspacefs.AttachmentHostDirectory, Native: []byte(`{}`)}, c.createErr
}
func (c *workspaceControl) Observe(ctx context.Context, r workspacefs.Reference) (workspacefs.Attachment, error) {
	return c.Create(ctx, r, 0)
}
func (c *workspaceControl) Delete(context.Context, workspacefs.Reference) error {
	c.deletes++
	return c.deleteErr
}

type workspaceLease struct{ err error }

func (l *workspaceLease) CheckOwnership(context.Context) error { return l.err }
func fixtureWorkspace() (*ExecutionOperations, *memoryWorkspace, *workspaceControl, *workspaceLease) {
	s := &memoryWorkspace{active: workspacefs.ConfigurationRecord{Configuration: workspacefs.Configuration{ID: uuid.NewString(), Adapter: "fixture", Parameters: []byte(`{}`)}}}
	c := &workspaceControl{store: s}
	l := &workspaceLease{}
	return NewExecution(s, s, c, l), s, c, l
}

var requiredWorkspace = workspacefs.Requirements{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}

func TestEnsureConvergesOriginalBindingAcrossSelectionChange(t *testing.T) {
	e, s, c, _ := fixtureWorkspace()
	tenant, environment := uuid.NewString(), uuid.NewString()
	c.createErr = workspacefs.ErrUnconfirmed
	if _, err := e.Ensure(t.Context(), tenant, environment, &requiredWorkspace, 0); !errors.Is(err, workspacefs.ErrUnconfirmed) {
		t.Fatal(err)
	}
	original := *s.record
	s.active.ID = uuid.NewString()
	c.createErr = nil
	binding, err := e.Ensure(t.Context(), tenant, environment, &requiredWorkspace, 0)
	if err != nil || binding.Configuration.ID != original.Configuration.ID || binding.Attachment.Reference != original.Reference || c.creates != 2 {
		t.Fatalf("binding=%+v creates=%d err=%v", binding, c.creates, err)
	}
	if _, err = e.Ensure(t.Context(), tenant, environment, &requiredWorkspace, 0); err != nil || c.creates != 2 {
		t.Fatal("ready binding recreated", err)
	}
}

func TestEnsureValidatesBeforeBinding(t *testing.T) {
	e, s, c, _ := fixtureWorkspace()
	if _, err := e.Ensure(t.Context(), uuid.NewString(), uuid.NewString(), &requiredWorkspace, 1024); !errors.Is(err, workspacefs.ErrUnsupported) || s.record != nil || c.creates != 0 {
		t.Fatal("unsupported request bound a workspace", err)
	}
	if _, err := e.Ensure(t.Context(), uuid.NewString(), uuid.NewString(), nil, 0); !errors.Is(err, workspacefs.ErrUnsupported) || s.record != nil {
		t.Fatal("missing requirements bound a workspace", err)
	}
}

func TestEnsureFencesConcurrentSelectionChange(t *testing.T) {
	e, s, c, _ := fixtureWorkspace()
	s.beforeBind = func() { s.active.ID = uuid.NewString() }
	if _, err := e.Ensure(t.Context(), uuid.NewString(), uuid.NewString(), &requiredWorkspace, 0); !errors.Is(err, ErrConflict) || s.record != nil || c.creates != 0 {
		t.Fatal("binding used a configuration that was no longer selected", err)
	}
}

func TestRetainedCapacitySurvivesDeploymentChange(t *testing.T) {
	e, s, c, _ := fixtureWorkspace()
	c.quota = true
	tenant, environment := uuid.NewString(), uuid.NewString()
	c.createErr = workspacefs.ErrUnconfirmed
	if _, err := e.Ensure(t.Context(), tenant, environment, &requiredWorkspace, 2048); !errors.Is(err, workspacefs.ErrUnconfirmed) || s.record.CapacityMiB != 2048 {
		t.Fatal(err)
	}
	c.createErr = nil
	if _, err := e.Ensure(t.Context(), tenant, environment, &requiredWorkspace, 4096); err != nil {
		t.Fatal(err)
	}
	// Resume validates the retained bound against the record's configuration
	// declaration, never a deployment request.
	c.quota = false
	if _, err := e.GetReady(t.Context(), tenant, environment, &requiredWorkspace); !errors.Is(err, workspacefs.ErrUnsupported) {
		t.Fatal("bounded workspace resolved by a configuration without quota", err)
	}
	c.quota = true
	if _, err := e.GetReady(t.Context(), tenant, environment, &requiredWorkspace); err != nil {
		t.Fatal(err)
	}
	if len(c.capacities) != 2 || c.capacities[0] != 2048 || c.capacities[1] != 2048 {
		t.Fatalf("Create capacities %v", c.capacities)
	}
}

func TestReadyBindingCarriesNoCredential(t *testing.T) {
	e, s, _, _ := fixtureWorkspace()
	s.active.Credential = []byte(`{"ssh_private_key":"synthetic-private-key"}`)
	binding, err := e.Ensure(t.Context(), uuid.NewString(), uuid.NewString(), &requiredWorkspace, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(binding)
	if strings.Contains(string(raw), "synthetic-private-key") {
		t.Fatalf("binding leaks credential: %s", raw)
	}
}

func TestWorkspaceDeletionRequiresExplicitIntentAndReleasedCompute(t *testing.T) {
	e, s, c, _ := fixtureWorkspace()
	_, err := e.Ensure(t.Context(), uuid.NewString(), uuid.NewString(), &requiredWorkspace, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.expired = true
	if _, err = e.DeleteBatch(t.Context(), ""); err != nil || c.deletes != 0 {
		t.Fatal("expiry deleted filesystem", err)
	}
	s.deletedSession = true
	s.allocation = true
	if _, err = e.DeleteBatch(t.Context(), ""); err != nil || c.deletes != 0 {
		t.Fatal("live compute deleted filesystem", err)
	}
	s.allocation = false
	c.deleteErr = workspacefs.ErrUnconfirmed
	cursor, err := e.DeleteBatch(t.Context(), "")
	if err != nil || s.record.State != Deleting || cursor != s.record.Reference.ObjectID {
		t.Fatal("unknown deletion settled", err)
	}
	c.deleteErr = nil
	if _, err = e.DeleteBatch(t.Context(), ""); err != nil || s.record.State != Deleted || s.record.Attachment == nil {
		t.Fatal("terminal metadata missing", err)
	}
}
func TestWorkspaceDeletionAfterUnconfirmedCreationWithoutAllocation(t *testing.T) {
	e, s, c, _ := fixtureWorkspace()
	c.createErr = workspacefs.ErrUnconfirmed
	_, _ = e.Ensure(t.Context(), uuid.NewString(), uuid.NewString(), &requiredWorkspace, 0)
	s.deletedSession = true
	if _, err := e.DeleteBatch(t.Context(), ""); err != nil || s.record.State != Deleted {
		t.Fatal("deleted Session creation did not converge", err)
	}
}
func TestWorkspaceLeaseLossAndResumeNeverCreate(t *testing.T) {
	e, s, c, l := fixtureWorkspace()
	tenant, environment := uuid.NewString(), uuid.NewString()
	if _, err := e.GetReady(t.Context(), tenant, environment, &requiredWorkspace); err != nil || c.creates != 0 {
		t.Fatal("resume created workspace", err)
	}
	l.err = errors.New("lease lost")
	if _, err := e.Ensure(t.Context(), tenant, environment, &requiredWorkspace, 0); !errors.Is(err, l.err) || c.creates != 0 {
		t.Fatal("unfenced create", err)
	}
	l.err = nil
	s.record.State = Deleted
	if _, err := e.Ensure(t.Context(), tenant, environment, &requiredWorkspace, 0); !errors.Is(err, ErrConflict) || c.creates != 0 {
		t.Fatal("recreated deleted identity", err)
	}
}

func TestGetReadyRetainsOriginalSelection(t *testing.T) {
	e, s, c, _ := fixtureWorkspace()
	tenant, environment := uuid.NewString(), uuid.NewString()
	binding, err := e.Ensure(t.Context(), tenant, environment, &requiredWorkspace, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.active.ID = uuid.NewString()
	resumed, err := e.GetReady(t.Context(), tenant, environment, &requiredWorkspace)
	if err != nil || resumed.Configuration.ID != binding.Configuration.ID || resumed.Attachment.Reference != binding.Attachment.Reference || c.creates != 1 {
		t.Fatal("resume changed binding", err)
	}
	if _, err = e.GetReady(t.Context(), uuid.NewString(), environment, &requiredWorkspace); !errors.Is(err, workspacefs.ErrOwnership) {
		t.Fatal("foreign binding accepted", err)
	}
}

func selectionInput(c workspacefs.Configuration, tenantCapacity uint32, credential json.RawMessage) SelectionInput {
	return SelectionInput{Selection: Selection{Configuration: c, TenantCapacityMiB: tenantCapacity}, Credential: credential}
}

func TestConfigureChecksBeforeLeaseFencedSelection(t *testing.T) {
	e, s, c, l := fixtureWorkspace()
	previous := s.active.ID
	next := s.active.Configuration
	next.ID = uuid.NewString()
	if _, err := e.Configure(t.Context(), selectionInput(next, 0, nil), func(workspacefs.Declaration) error { return workspacefs.ErrUnsupported }); !errors.Is(err, workspacefs.ErrUnsupported) || s.active.ID != previous || c.checks != 0 {
		t.Fatal("unsupported selection reached native check", err)
	}
	c.checkErr = workspacefs.ErrUnavailable
	if _, err := e.Configure(t.Context(), selectionInput(next, 0, nil), func(workspacefs.Declaration) error { return nil }); !errors.Is(err, workspacefs.ErrUnavailable) || s.active.ID != previous {
		t.Fatal("failed native check published selection", err)
	}
	c.checkErr = nil
	l.err = errors.New("lease lost")
	if _, err := e.Configure(t.Context(), selectionInput(next, 0, nil), func(workspacefs.Declaration) error { return nil }); !errors.Is(err, l.err) || s.active.ID != previous {
		t.Fatal("lost lease published selection", err)
	}
	l.err = nil
	got, err := e.Configure(t.Context(), selectionInput(next, 0, nil), func(workspacefs.Declaration) error { return nil })
	if err != nil || got.ID != next.ID || s.active.ID != next.ID {
		t.Fatal("unconfigured Sandbox blocked filesystem selection", err)
	}
}

func TestConfigureTenantCapacityRequiresQuota(t *testing.T) {
	e, s, c, _ := fixtureWorkspace()
	next := workspacefs.Configuration{ID: uuid.NewString(), Adapter: "fixture", Parameters: []byte(`{}`)}
	accept := func(workspacefs.Declaration) error { return nil }
	if _, err := e.Configure(t.Context(), selectionInput(next, 4096, nil), accept); !errors.Is(err, workspacefs.ErrUnsupported) || s.active.ID == next.ID || c.checks != 0 {
		t.Fatal("tenant capacity accepted without quota enforcement", err)
	}
	c.quota = true
	if _, err := e.Configure(t.Context(), selectionInput(next, 0, nil), accept); !errors.Is(err, workspacefs.ErrUnsupported) || s.active.ID == next.ID || c.checks != 0 {
		t.Fatal("quota configuration selected without tenant capacity", err)
	}
	got, err := e.Configure(t.Context(), selectionInput(next, 4096, nil), accept)
	if err != nil || got.TenantCapacityMiB != 4096 || s.tenantCapacity != 4096 || s.active.ID != next.ID {
		t.Fatal("tenant capacity not selected with configuration", err)
	}
}

func TestConfigureRetainsStoredCredentialOnlyWhenOmitted(t *testing.T) {
	e, s, c, _ := fixtureWorkspace()
	stored := json.RawMessage(`{"ssh_private_key":"stored"}`)
	existing := workspacefs.Configuration{ID: uuid.NewString(), Adapter: "fixture", Parameters: []byte(`{}`)}
	s.retained = map[string]workspacefs.ConfigurationRecord{existing.ID: {Configuration: existing, Credential: stored}}
	accept := func(workspacefs.Declaration) error { return nil }
	if _, err := e.Configure(t.Context(), selectionInput(existing, 0, nil), accept); err != nil || string(s.active.Credential) != string(stored) {
		t.Fatal("omitted credential did not retain the stored one", err)
	}
	supplied := json.RawMessage(`{"ssh_private_key":"supplied"}`)
	if _, err := e.Configure(t.Context(), selectionInput(existing, 0, supplied), accept); err != nil || string(c.normalized[1]) != string(supplied) {
		t.Fatal("supplied credential was not normalized", err)
	}
	fresh := workspacefs.Configuration{ID: uuid.NewString(), Adapter: "fixture", Parameters: []byte(`{}`)}
	if _, err := e.Configure(t.Context(), selectionInput(fresh, 0, nil), accept); err != nil || c.normalized[2] != nil {
		t.Fatal("new configuration received a credential", err)
	}
}
