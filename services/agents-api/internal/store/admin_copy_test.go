package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentplugin"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
)

func adminCopyFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{52}, 32))
	if err != nil {
		t.Fatal(err)
	}
	source, target := uuid.NewString(), uuid.NewString()
	for _, tenant := range []string{source, target} {
		if _, err := pool.Exec(t.Context(), "INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'copy-test',$2)", tenant, tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), "INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Copy fixture',$1,'service_account',$2)", tenant, "project:"+tenant); err != nil {
			t.Fatal(err)
		}
	}
	return NewWithCredentialCipher(pool, cipher), source, target
}

func adminCopyContext(t *testing.T, projectID string) context.Context {
	t.Helper()
	return adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "copy-admin", ActorLabel: "copy-test", RequestID: uuid.NewString(), TraceID: uuid.NewString(), ProjectID: projectID})
}

func copiedID(t *testing.T, result CopyAssetsResult, kind, source string) string {
	t.Helper()
	for _, mapping := range result.Mappings {
		if mapping.Type == kind && mapping.SourceID == source {
			return mapping.TargetID
		}
	}
	t.Fatalf("missing %s mapping", kind)
	return ""
}

func TestAdminCopyFilesRetryIsolationAndAuditRollback(t *testing.T) {
	s, source, target := adminCopyFixture(t)
	data := bytes.Repeat([]byte("private-copy-file-canary"), 40000)
	file, err := s.CreateSourceFile(t.Context(), source, uploadSource(data))
	if err != nil {
		t.Fatal(err)
	}
	input := CopyAssetsInput{ResourceType: "file", ResourceID: file.ID, IdempotencyKey: uuid.NewString()}
	count := sourceObjectCount(t, s.pool)
	var results [2]CopyAssetsResult
	var failures [2]error
	var group sync.WaitGroup
	for i := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			results[i], failures[i] = s.CopyAssets(adminCopyContext(t, target), source, target, input)
		}()
	}
	group.Wait()
	for _, err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(results[0], results[1]) || sourceObjectCount(t, s.pool) != count+1 {
		t.Fatal("concurrent replay duplicated copy")
	}
	targetID := copiedID(t, results[0], "file", file.ID)
	if err := s.ReadSourceFile(t.Context(), target, targetID, func(_ SourceFile, r io.Reader) error {
		body, err := io.ReadAll(r)
		if !bytes.Equal(body, data) {
			t.Error("file copy changed contents")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSourceFile(t.Context(), source, targetID); !errors.Is(err, ErrNotFound) {
		t.Fatal("target leaked to source", err)
	}
	changed := input
	changed.IncludeDependencies = true
	if _, err := s.CopyAssets(adminCopyContext(t, target), source, target, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed retry accepted", err)
	}
	if _, err := s.CopyAssets(adminCopyContext(t, source), target, source, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign source accepted", err)
	}
	if _, err := s.CopyAssets(adminCopyContext(t, target), source, source, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("same tenant accepted", err)
	}
	var owners, audits, publicOwners int
	for query, out := range map[string]*int{
		"SELECT count(*) FROM admin_resource_owners WHERE tenant_id=$1": &owners,
		"SELECT count(*) FROM admin_audit_log WHERE tenant_id=$1":       &audits,
		"SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1":    &publicOwners,
	} {
		if err := s.pool.QueryRow(t.Context(), query, target).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	if owners != 1 || audits != 1 || publicOwners != 0 {
		t.Fatal("incorrect administrator ownership", owners, audits, publicOwners)
	}
	// A real database trigger fails only this target's audit after the LO and row
	// have been written, proving that both are owned by the same transaction.
	function := "copy_audit_" + uuid.NewString()[:8]
	ddl := "CREATE FUNCTION " + function + "() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.tenant_id='" + target + "'::uuid THEN RAISE EXCEPTION 'controlled copy audit failure'; END IF; RETURN NEW; END $$"
	if _, err := s.pool.Exec(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "CREATE TRIGGER "+function+" BEFORE INSERT ON admin_audit_log FOR EACH ROW EXECUTE FUNCTION "+function+"()"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS "+function+" ON admin_audit_log")
		_, _ = s.pool.Exec(context.Background(), "DROP FUNCTION IF EXISTS "+function+"()")
	})
	input.IdempotencyKey = uuid.NewString()
	if _, err := s.CopyAssets(adminCopyContext(t, target), source, target, input); err == nil {
		t.Fatal("audit failure committed")
	}
	if sourceObjectCount(t, s.pool) != count+1 {
		t.Fatal("rolled back LO leaked")
	}
	var copies int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM source_files WHERE tenant_id=$1", target).Scan(&copies); err != nil || copies != 1 {
		t.Fatal("rolled back metadata leaked", copies, err)
	}
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM admin_asset_copies WHERE target_tenant_id=$1", target).Scan(&copies); err != nil || copies != 1 {
		t.Fatal("rolled back idempotency leaked", copies, err)
	}
}

func TestAdminCopySkillVersionsAndTemplateConfidentialDependencies(t *testing.T) {
	s, source, target := adminCopyFixture(t)
	archive := skillArchive(t, "copy-skill-secret")
	skill, err := s.CreateSkill(t.Context(), source, archive)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.CreateSkillVersion(t.Context(), source, skill.ID, archive, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DeleteSkillVersion(t.Context(), source, skill.ID, "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSkillDefault(t.Context(), source, skill.ID, "3"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteSkillVersion(t.Context(), source, skill.ID, "4"); err != nil {
		t.Fatal(err)
	}
	file, err := s.CreateSourceFile(t.Context(), source, uploadSource([]byte("copy-template-file")))
	if err != nil {
		t.Fatal(err)
	}
	var pluginArchive bytes.Buffer
	writer := zip.NewWriter(&pluginArchive)
	for path, body := range map[string]string{"proof/.codex-plugin/plugin.json": `{"name":"copy-plugin","description":"Copy proof.","skills":"./skills"}`, "proof/skills/proof/SKILL.md": "---\nname: proof\ndescription: Copy proof.\n---\nplugin-copy-secret"} {
		f, err := writer.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	setup := EnvironmentSetup{Env: map[string]string{"PRIVATE_VALUE": "copy-env-secret"}, Commands: []SetupCommand{{Command: "printf copy-setup-secret"}}, Skills: []EnvironmentSkill{{Metadata: EnvironmentSkillMetadata{Type: "skill_reference", SkillID: skill.ID, Version: "3"}}}, Plugins: []EnvironmentPlugin{{Metadata: agentplugin.Metadata{Type: "inline", Name: "copy-plugin", Description: "Copy proof."}, Archive: pluginArchive.Bytes()}}, CapabilityDirectories: []string{"/workspace/tools"}}
	template, err := s.CreateEnvironmentTemplate(t.Context(), source, EnvironmentTemplateInput{Initialization: setup, Files: []InitialFile{{Type: "file_id", Path: "/workspace/source.txt", FileID: file.ID}, {Type: "inline", Path: "/workspace/inline.txt", Data: []byte("copy-inline-secret")}}})
	if err != nil {
		t.Fatal(err)
	}
	input := CopyAssetsInput{ResourceType: "environment_template", ResourceID: template.ID}
	if _, err := s.CopyAssets(adminCopyContext(t, target), source, target, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("dependencies silently omitted", err)
	}
	input.IncludeDependencies = true
	result, err := s.CopyAssets(adminCopyContext(t, target), source, target, input)
	if err != nil {
		t.Fatal(err)
	}
	copySkill := copiedID(t, result, "skill", skill.ID)
	meta, err := s.GetSkill(t.Context(), target, copySkill)
	if err != nil || meta.DefaultVersion != 3 || meta.LatestVersion != 3 {
		t.Fatal("version pointers changed", meta, err)
	}
	if _, _, err := s.ReadSkillVersion(t.Context(), target, copySkill, "2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("version gap lost", err)
	}
	for _, version := range []string{"1", "3"} {
		_, got, err := s.ReadSkillVersion(t.Context(), target, copySkill, version)
		if err != nil || !bytes.Equal(got, archive) {
			t.Fatal("copied archive cannot decrypt", err)
		}
	}
	next, err := s.CreateSkillVersion(t.Context(), target, copySkill, archive, false)
	if err != nil || next.Version != 5 {
		t.Fatal("next version reused deleted version", next.Version, err)
	}
	resolved, files, err := s.ResolveEnvironmentTemplate(t.Context(), target, copiedID(t, result, "environment_template", template.ID))
	if err != nil {
		t.Fatal(err)
	}
	setup.Skills[0].Metadata.SkillID = copySkill
	setup.Packages = setup.PackageMetadata()
	if !reflect.DeepEqual(resolved.Initialization, setup) || len(files) != 2 || files[0].FileID != copiedID(t, result, "file", file.ID) || string(files[1].Data) != "copy-inline-secret" {
		t.Fatal("confidential template fields or references changed")
	}
	raw, _ := json.Marshal(result)
	for _, secret := range []string{"copy-env-secret", "copy-setup-secret", "plugin-copy-secret", "copy-inline-secret", "copy-skill-secret"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("response exposed secret")
		}
	}
	// Fail after the Skill has been copied to prove dependency rollback.
	missing := EnvironmentTemplateInput{Initialization: EnvironmentSetup{Skills: setup.Skills}, Files: []InitialFile{{Type: "file_id", Path: "/workspace/missing", FileID: "file-" + uuid.NewString()}}}
	missing.Initialization.Skills[0].Metadata.SkillID = skill.ID
	broken, err := s.CreateEnvironmentTemplate(t.Context(), source, missing)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM skills WHERE tenant_id=$1", target).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CopyAssets(adminCopyContext(t, target), source, target, CopyAssetsInput{ResourceType: "environment_template", ResourceID: broken.ID, IncludeDependencies: true}); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing dependency accepted", err)
	}
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM skills WHERE tenant_id=$1", target).Scan(&after); err != nil || before != after {
		t.Fatal("dependency copy escaped rollback", err)
	}
}

func TestAdminCopyArchivedProjectBoundaries(t *testing.T) {
	s, source, target := adminCopyFixture(t)
	file, err := s.CreateSourceFile(t.Context(), source, uploadSource([]byte("retained project asset")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveProject(adminCopyContext(t, source), source); err != nil {
		t.Fatal(err)
	}
	input := CopyAssetsInput{ResourceType: "file", ResourceID: file.ID, IdempotencyKey: uuid.NewString()}
	result, err := s.CopyAssets(adminCopyContext(t, target), source, target, input)
	if err != nil {
		t.Fatal("archived source was not readable", err)
	}
	targetID := copiedID(t, result, "file", file.ID)
	if _, err := s.ArchiveProject(adminCopyContext(t, target), target); err != nil {
		t.Fatal(err)
	}
	before := adminMutationSnapshot(t, s, "source_files", "admin_asset_copies", "admin_resource_owners", "admin_audit_log", "pg_largeobject")
	for _, key := range []string{input.IdempotencyKey, uuid.NewString()} {
		input.IdempotencyKey = key
		if _, err := s.CopyAssets(adminCopyContext(t, target), source, target, input); !errors.Is(err, ErrProjectArchived) {
			t.Fatal("archived target accepted a copy or replay", err)
		}
	}
	if !reflect.DeepEqual(before, adminMutationSnapshot(t, s, "source_files", "admin_asset_copies", "admin_resource_owners", "admin_audit_log", "pg_largeobject")) {
		t.Fatal("rejected copy changed retained resources or audit")
	}
	if _, err := s.GetSourceFile(t.Context(), target, targetID); err != nil {
		t.Fatal("archive lost the existing copy", err)
	}
}
