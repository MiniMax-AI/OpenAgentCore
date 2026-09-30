package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/google/uuid"
)

func TestAdminProjectAndKeyDatabaseAuditTransactions(t *testing.T) {
	s, _ := newManagedTestStore(t)
	rejectAdminAuditInsert(t, s)
	for _, action := range []string{"project_create", "rename", "archive", "key_create", "revoke"} {
		t.Run(action, func(t *testing.T) {
			projectID, keyID := uuid.NewString(), uuid.NewString()
			var p Project
			var issued IssuedProjectAPIKey
			var asset SavedAgent
			ctxFor := func(request string) context.Context {
				return adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "87654321", ActorLabel: "administrator fixture", ProjectID: projectID, RequestID: request, TraceID: "admin-mutation-trace"})
			}
			if action != "project_create" {
				var err error
				p, err = s.CreateProject(ctxFor(uuid.NewString()), projectID, "original")
				if err != nil {
					t.Fatal(err)
				}
				asset, err = s.CreateAgent(t.Context(), p.TenantID, CreateAgentInput{Configuration: []byte(`{"model":"fixture"}`)})
				if err != nil {
					t.Fatal(err)
				}
				if action == "archive" || action == "revoke" {
					issued, err = s.CreateProjectAPIKey(ctxFor(uuid.NewString()), projectID, keyID, "original key")
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			mutate := func(request string) (Project, IssuedProjectAPIKey, error) {
				ctx := ctxFor(request)
				switch action {
				case "project_create":
					v, e := s.CreateProject(ctx, projectID, "created")
					return v, IssuedProjectAPIKey{}, e
				case "rename":
					v, e := s.RenameProject(ctx, projectID, "renamed")
					return v, IssuedProjectAPIKey{}, e
				case "archive":
					v, e := s.ArchiveProject(ctx, projectID)
					return v, IssuedProjectAPIKey{}, e
				case "key_create":
					v, e := s.CreateProjectAPIKey(ctx, projectID, keyID, "created key")
					return Project{}, v, e
				default:
					return Project{}, IssuedProjectAPIKey{}, s.RevokeProjectAPIKey(ctx, projectID, keyID)
				}
			}
			tables := []string{"execution_project_scopes", "projects", "project_api_keys", "agents", "admin_audit_log", "write_audit_operations", "write_audit_owners"}
			before := adminMutationSnapshot(t, s, tables...)
			rejections := adminAuditRejections(t, s)
			rejectedProject, rejectedKey, err := mutate(rejectedAdminRequest)
			requireAdminAuditFailure(t, s, err, rejections)
			if rejectedProject.ID != "" || rejectedKey.Key != "" {
				t.Fatal("failed transaction exposed an uncommitted resource")
			}
			if !reflect.DeepEqual(before, adminMutationSnapshot(t, s, tables...)) {
				t.Fatal("failed audit changed Project, key, asset, or audit state")
			}
			if issued.Key != "" {
				if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(issued.Key)); err != nil {
					t.Fatal("failed audit invalidated original key", err)
				}
			}
			request := uuid.NewString()
			result, key, err := mutate(request)
			if err != nil {
				t.Fatal(err)
			}
			if action == "project_create" {
				p = result
			}
			expectedAction, kind, resource := action, "project", projectID
			switch action {
			case "project_create":
				expectedAction = "create"
			case "key_create":
				expectedAction = "create"
				kind = "api_key"
				resource = keyID
			case "revoke":
				kind = "api_key"
				resource = keyID
			}
			var count int
			if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_audit_log WHERE project_id=$1 AND tenant_id=$2 AND request_id=$3 AND action=$4 AND resource_type=$5 AND resource_id=$6 AND admin_credential_id='87654321' AND actor_label='administrator fixture' AND trace_id='admin-mutation-trace'`, projectID, p.TenantID, request, expectedAction, kind, resource).Scan(&count); err != nil || count != 1 {
				t.Fatal("successful mutation lacks matching audit", err)
			}
			if action == "key_create" {
				if key.Key == "" {
					t.Fatal("issuance lacks plaintext")
				}
				if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(key.Key)); err != nil {
					t.Fatal(err)
				}
			}
			if action == "archive" || action == "revoke" {
				if _, err := s.ResolveProjectAPIKey(t.Context(), projectKeyDigest(issued.Key)); !errors.Is(err, ErrNotFound) {
					t.Fatal("invalidated key authenticated", err)
				}
			}
			if action != "project_create" {
				if _, err := s.GetAgent(t.Context(), p.TenantID, asset.ID); err != nil {
					t.Fatal("management mutation removed assets", err)
				}
			}
		})
	}
}
