package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/google/uuid"
)

func TestAdminKeyDatabaseAuditTransactions(t *testing.T) {
	s, _ := newManagedTestStore(t)
	rejectAdminAuditInsert(t, s)
	for _, action := range []string{"create", "reset", "revoke"} {
		t.Run(action, func(t *testing.T) {
			id := uuid.NewString()
			var issued IssuedProjectAPIKey
			var asset SavedAgent
			tenant := uuid.NewString()
			ctxFor := func(request string) context.Context {
				ctx := adminDeleteContext(t.Context(), tenant, request)
				source, _ := adminaudit.FromContext(ctx)
				source.TargetKeyID = id
				return adminaudit.WithSource(ctx, source)
			}
			if action != "create" {
				var err error
				issued, err = s.CreateProjectAPIKey(ctxFor(uuid.NewString()), id, "administrator audit rollback")
				if err != nil {
					t.Fatal(err)
				}
				tenant = issued.TenantID
				asset, err = s.CreateAgent(t.Context(), tenant, CreateAgentInput{Configuration: []byte(`{"model":"fixture"}`)})
				if err != nil {
					t.Fatal(err)
				}
			}
			mutate := func(request string) (IssuedProjectAPIKey, error) {
				switch action {
				case "create":
					return s.CreateProjectAPIKey(ctxFor(request), id, "administrator audit rollback")
				case "reset":
					return s.ResetProjectAPIKey(ctxFor(request), id, request)
				default:
					return IssuedProjectAPIKey{}, s.RevokeProjectAPIKey(ctxFor(request), id)
				}
			}
			tables := []string{"execution_project_scopes", "project_api_keys", "agents", "admin_audit_log", "write_audit_operations", "write_audit_owners"}
			before := adminMutationSnapshot(t, s, tables...)
			rejections := adminAuditRejections(t, s)
			rejected, err := mutate(rejectedAdminRequest)
			requireAdminAuditFailure(t, s, err, rejections)
			if rejected.Key != "" || rejected.TenantID != "" {
				t.Fatal("failed audit exposed an uncommitted key")
			}
			if !reflect.DeepEqual(before, adminMutationSnapshot(t, s, tables...)) {
				t.Fatal("failed administrator audit changed scope, secret, revocation, assets or audit")
			}
			if action == "create" {
				if _, err := s.GetProjectAPIKey(t.Context(), id); !errors.Is(err, ErrNotFound) {
					t.Fatal("failed key creation survived rollback", err)
				}
			} else {
				if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(issued.Key)); err != nil {
					t.Fatal("failed audit invalidated the original secret", err)
				}
				if _, err := s.GetAgent(t.Context(), tenant, asset.ID); err != nil {
					t.Fatal("failed audit lost existing assets", err)
				}
			}
			request := uuid.NewString()
			result, err := mutate(request)
			if err != nil {
				t.Fatal(err)
			}
			if action == "create" {
				tenant = result.TenantID
			}
			assertAdminMutationAudit(t, s, tenant, request, action, "api_key", id)
			metadata, err := s.GetProjectAPIKey(t.Context(), id)
			if err != nil || metadata.Key.ID != id || metadata.Key.TenantID != tenant {
				t.Fatal("key identity or space changed", err)
			}
			switch action {
			case "create":
				if result.Key == "" {
					t.Fatal("successful key creation did not return its one-time secret")
				}
				if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(result.Key)); err != nil {
					t.Fatal(err)
				}
			case "reset":
				if result.Key == "" || result.Key == issued.Key {
					t.Fatal("reset did not replace the secret")
				}
				if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(issued.Key)); !errors.Is(err, ErrNotFound) {
					t.Fatal("old secret still authenticated", err)
				}
				if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(result.Key)); err != nil {
					t.Fatal("new secret did not authenticate", err)
				}
			case "revoke":
				if metadata.Key.RevokedAt == nil {
					t.Fatal("successful revocation did not persist")
				}
				if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(issued.Key)); !errors.Is(err, ErrNotFound) {
					t.Fatal("revoked secret still authenticated", err)
				}
			}
			if action != "create" {
				if _, err := s.GetAgent(t.Context(), tenant, asset.ID); err != nil {
					t.Fatal("key operation changed existing assets", err)
				}
			}
		})
	}
}
