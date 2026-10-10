package workspaces

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/google/uuid"
)

type memoryWorkspace struct {
	active         workspacefs.Configuration
	record         *Record
	deletedSession bool
	allocation     bool
	expired        bool
}

func (s *memoryWorkspace) ActiveConfiguration(context.Context) (workspacefs.Configuration, error) {
	if s.active.ID == "" {
		return s.active, ErrNotConfigured
	}
	return s.active, nil
}
func (s *memoryWorkspace) SelectConfiguration(_ context.Context, c workspacefs.Configuration) error {
	s.active = c
	return nil
}
func (s *memoryWorkspace) Get(context.Context, string, string) (Record, error) {
	if s.record == nil {
		return Record{}, ErrNotFound
	}
	return *s.record, nil
}
func (s *memoryWorkspace) Bind(_ context.Context, tenant, environment string) (Record, error) {
	if s.record != nil {
		return *s.record, nil
	}
	if s.active.ID == "" {
		return Record{}, ErrNotConfigured
	}
	s.record = &Record{Reference: workspacefs.Reference{TenantID: tenant, EnvironmentID: environment, ObjectID: uuid.NewString()}, Configuration: s.active, State: Creating}
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
	createErr, deleteErr error
	creates, deletes     int
	checks               int
	checkErr             error
}

func (c *workspaceControl) Control(config workspacefs.Configuration) (workspacefs.Control, error) {
	if c.store.record != nil && config.ID != c.store.record.Configuration.ID {
		return nil, workspacefs.ErrOwnership
	}
	return c, nil
}
func (c *workspaceControl) Normalize(cn workspacefs.Configuration) (workspacefs.Configuration, error) {
	return cn, nil
}
func (c *workspaceControl) Check(context.Context) error { c.checks++; return c.checkErr }
func (c *workspaceControl) Declaration() workspacefs.Declaration {
	return workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}
}
func (c *workspaceControl) Create(_ context.Context, r workspacefs.Reference) (workspacefs.Attachment, error) {
	c.creates++
	if c.store.record == nil || c.store.record.Reference != r {
		panic("create before durable bind")
	}
	return workspacefs.Attachment{Reference: r, ConfigurationID: c.store.record.Configuration.ID, Kind: workspacefs.AttachmentHostDirectory, Native: []byte(`{}`)}, c.createErr
}
func (c *workspaceControl) Observe(ctx context.Context, r workspacefs.Reference) (workspacefs.Attachment, error) {
	return c.Create(ctx, r)
}
func (c *workspaceControl) Delete(context.Context, workspacefs.Reference) error {
	c.deletes++
	return c.deleteErr
}

type workspaceLease struct{ err error }

func (l *workspaceLease) CheckOwnership(context.Context) error { return l.err }
func fixtureWorkspace() (*ExecutionOperations, *memoryWorkspace, *workspaceControl, *workspaceLease) {
	s := &memoryWorkspace{active: workspacefs.Configuration{ID: uuid.NewString(), Adapter: "fixture", Parameters: []byte(`{}`)}}
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
	if _, err := e.GetReady(t.Context(), tenant, environment, &requiredWorkspace, 0); err != nil || c.creates != 0 {
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

func TestGetReadyRetainsOriginalSelectionAndValidatesCapacity(t *testing.T) {
	e, s, c, _ := fixtureWorkspace()
	tenant, environment := uuid.NewString(), uuid.NewString()
	if _, err := e.Ensure(t.Context(), tenant, environment, &requiredWorkspace, 1024); !errors.Is(err, workspacefs.ErrUnsupported) || c.creates != 0 {
		t.Fatal("unsupported quota created filesystem", err)
	}
	binding, err := e.Ensure(t.Context(), tenant, environment, &requiredWorkspace, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.active.ID = uuid.NewString()
	resumed, err := e.GetReady(t.Context(), tenant, environment, &requiredWorkspace, 0)
	if err != nil || resumed.Configuration.ID != binding.Configuration.ID || resumed.Attachment.Reference != binding.Attachment.Reference || c.creates != 1 {
		t.Fatal("resume changed binding", err)
	}
	if _, err = e.GetReady(t.Context(), uuid.NewString(), environment, &requiredWorkspace, 0); !errors.Is(err, workspacefs.ErrOwnership) {
		t.Fatal("foreign binding accepted", err)
	}
}

func TestConfigureChecksBeforeLeaseFencedSelection(t *testing.T) {
	e, s, c, l := fixtureWorkspace()
	previous := s.active.ID
	next := s.active
	next.ID = uuid.NewString()
	if _, err := e.Configure(t.Context(), next, func(workspacefs.Declaration) error { return workspacefs.ErrUnsupported }); !errors.Is(err, workspacefs.ErrUnsupported) || s.active.ID != previous || c.checks != 0 {
		t.Fatal("unsupported selection reached native check", err)
	}
	c.checkErr = workspacefs.ErrUnavailable
	if _, err := e.Configure(t.Context(), next, func(workspacefs.Declaration) error { return nil }); !errors.Is(err, workspacefs.ErrUnavailable) || s.active.ID != previous {
		t.Fatal("failed native check published selection", err)
	}
	c.checkErr = nil
	l.err = errors.New("lease lost")
	if _, err := e.Configure(t.Context(), next, func(workspacefs.Declaration) error { return nil }); !errors.Is(err, l.err) || s.active.ID != previous {
		t.Fatal("lost lease published selection", err)
	}
	l.err = nil
	got, err := e.Configure(t.Context(), next, func(workspacefs.Declaration) error { return nil })
	if err != nil || got.ID != next.ID || s.active.ID != next.ID {
		t.Fatal("unconfigured Sandbox blocked filesystem selection", err)
	}
}
