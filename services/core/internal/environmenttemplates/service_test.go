package environmenttemplates

import (
	"context"
	"errors"
	"testing"
)

type fakeStorage struct {
	t      *testing.T
	create func(context.Context, string, Input) (Template, error)
	update func(context.Context, string, string, Input) (Template, error)
	delete func(context.Context, string, string) (string, error)
}

func (f *fakeStorage) Create(ctx context.Context, tenantID string, in Input) (Template, error) {
	if f.create == nil {
		f.t.Fatal("unexpected call to Create")
	}
	return f.create(ctx, tenantID, in)
}

func (f *fakeStorage) Update(ctx context.Context, tenantID, templateID string, in Input) (Template, error) {
	if f.update == nil {
		f.t.Fatal("unexpected call to Update")
	}
	return f.update(ctx, tenantID, templateID, in)
}

func (f *fakeStorage) Delete(ctx context.Context, tenantID, templateID string) (string, error) {
	if f.delete == nil {
		f.t.Fatal("unexpected call to Delete")
	}
	return f.delete(ctx, tenantID, templateID)
}

func newService(t *testing.T, storage *fakeStorage) *Service {
	t.Helper()
	storage.t = t
	service, err := NewService(storage)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestNewServiceRequiresStorage(t *testing.T) {
	if _, err := NewService(nil); err == nil {
		t.Fatal("NewService(nil) succeeded")
	}
}

func TestCreateSavesDefaultNetwork(t *testing.T) {
	service := newService(t, &fakeStorage{create: func(_ context.Context, tenantID string, in Input) (Template, error) {
		if tenantID != "tenant" || !in.SetNetwork || in.NetworkAccess != "enabled" || in.AllowedDomains != nil || *in.Name != "name" {
			t.Fatalf("Create(%q, %+v)", tenantID, in)
		}
		return Template{ID: "template"}, nil
	}})
	got, err := service.Create(context.Background(), CreateCommand{TenantID: "tenant", Input: Input{Name: ptr("name")}})
	if err != nil || got.ID != "template" {
		t.Fatalf("Create() = %+v, %v", got, err)
	}
}

func TestInvalidWritesNeverReachStorage(t *testing.T) {
	service := newService(t, &fakeStorage{})
	invalid := Input{SetNetwork: true, NetworkAccess: "restricted"}
	if _, err := service.Create(context.Background(), CreateCommand{TenantID: "tenant", Input: invalid}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.Update(context.Background(), UpdateCommand{TenantID: "tenant", TemplateID: "missing", Input: invalid}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestUpdateKeepsUnsetNetwork(t *testing.T) {
	service := newService(t, &fakeStorage{update: func(_ context.Context, tenantID, templateID string, in Input) (Template, error) {
		if tenantID != "tenant" || templateID != "template" || in.SetNetwork || !in.SetName {
			t.Fatalf("Update(%q, %q, %+v)", tenantID, templateID, in)
		}
		return Template{ID: templateID}, nil
	}})
	if _, err := service.Update(context.Background(), UpdateCommand{TenantID: "tenant", TemplateID: "template", Input: Input{SetName: true}}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteReturnsStorageOutcome(t *testing.T) {
	service := newService(t, &fakeStorage{delete: func(_ context.Context, tenantID, templateID string) (string, error) {
		if tenantID != "tenant" || templateID != "missing" {
			t.Fatalf("Delete(%q, %q)", tenantID, templateID)
		}
		return "", ErrNotFound
	}})
	if _, err := service.Delete(context.Background(), DeleteCommand{TenantID: "tenant", TemplateID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() error = %v", err)
	}
}
