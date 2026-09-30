package templatepg_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/templatepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fixture is a Template adapter with the credential key and one without it,
// on the same database, plus the service that validates writes.
type fixture struct {
	pool    *pgxpool.Pool
	keyed   *templatepg.Store
	keyless *templatepg.Store
	service *environmenttemplates.Service
}

func newFixture(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{29}, 32))
	if err != nil {
		t.Fatal(err)
	}
	keyed := templatepg.New(pgunit.NewPool(pool), cipher)
	service, err := environmenttemplates.NewService(keyed)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{pool: pool, keyed: keyed, keyless: templatepg.New(pgunit.NewPool(pool), nil), service: service}
}

func (f fixture) create(t *testing.T, tenant string, in environmenttemplates.Input) environmenttemplates.Template {
	t.Helper()
	created, err := f.service.Create(t.Context(), environmenttemplates.CreateCommand{TenantID: tenant, Input: in})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func (f fixture) update(t *testing.T, tenant, id string, in environmenttemplates.Input) environmenttemplates.Template {
	t.Helper()
	updated, err := f.service.Update(t.Context(), environmenttemplates.UpdateCommand{TenantID: tenant, TemplateID: id, Input: in})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func (f fixture) resolve(t *testing.T, tenant, id string) environmenttemplates.Resolved {
	t.Helper()
	resolved, err := f.keyed.Resolve(t.Context(), tenant, id)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// ciphertext reads one confidential column and fails when it holds plaintext.
func (f fixture) requireSealed(t *testing.T, id, column, canary string) {
	t.Helper()
	var metadata, sealed []byte
	query := "SELECT to_jsonb(t) - '" + column + "', " + column + " FROM environment_templates t WHERE id=$1"
	if err := f.pool.QueryRow(t.Context(), query, id).Scan(&metadata, &sealed); err != nil || len(sealed) == 0 || bytes.Contains(metadata, []byte(canary)) || bytes.Contains(sealed, []byte(canary)) {
		t.Fatalf("%s is not sealed: %v", column, err)
	}
}

func ptr(value string) *string { return &value }

func TestTemplatesDurableIsolatedAndConcurrent(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	created := f.create(t, tenant, environmenttemplates.Input{Name: ptr(" template ")})
	if created.NetworkAccess != "enabled" || created.Name == nil || *created.Name != " template " || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Fatal(created)
	}
	if _, err := f.keyed.Get(ctx, foreign, created.ID); !errors.Is(err, environmenttemplates.ErrNotFound) {
		t.Fatal("foreign read", err)
	}
	if _, err := f.service.Update(ctx, environmenttemplates.UpdateCommand{TenantID: foreign, TemplateID: created.ID, Input: environmenttemplates.Input{SetName: true}}); !errors.Is(err, environmenttemplates.ErrNotFound) {
		t.Fatal("foreign update", err)
	}
	if _, err := f.service.Delete(ctx, environmenttemplates.DeleteCommand{TenantID: foreign, TemplateID: created.ID}); !errors.Is(err, environmenttemplates.ErrNotFound) {
		t.Fatal("foreign delete", err)
	}
	if _, err := f.keyed.List(ctx, foreign, environmenttemplates.ListQuery{After: created.ID, Limit: 1}); !errors.Is(err, environmenttemplates.ErrNotFound) {
		t.Fatal("foreign cursor", err)
	}
	var wg sync.WaitGroup
	for _, in := range []environmenttemplates.Input{{Name: ptr("changed"), SetName: true}, {NetworkAccess: "disabled", SetNetwork: true}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.service.Update(ctx, environmenttemplates.UpdateCommand{TenantID: tenant, TemplateID: created.ID, Input: in}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := f.keyed.Get(ctx, tenant, created.ID)
	if err != nil || got.NetworkAccess != "disabled" || got.Name == nil || *got.Name != "changed" || !got.CreatedAt.Equal(created.CreatedAt) {
		t.Fatal("lost concurrent update", got, err)
	}
	f.pool.Close()
	f = newFixture(t, pgtest.Open(t))
	if got, err = f.keyed.Get(ctx, tenant, created.ID); err != nil || got.NetworkAccess != "disabled" {
		t.Fatal("lost durable template", got, err)
	}
	ids := []string{created.ID}
	for range 3 {
		ids = append(ids, f.create(t, tenant, environmenttemplates.Input{}).ID)
	}
	first, err := f.keyed.List(ctx, tenant, environmenttemplates.ListQuery{Limit: 2, Ascending: true})
	if err != nil || !first.HasMore || len(first.Templates) != 2 || first.Templates[0].ID != ids[0] {
		t.Fatal(first, err)
	}
	second, err := f.keyed.List(ctx, tenant, environmenttemplates.ListQuery{After: first.Templates[1].ID, Limit: 2, Ascending: true})
	if err != nil || second.HasMore || len(second.Templates) != 2 || second.Templates[0].ID != ids[2] {
		t.Fatal(second, err)
	}
	reverse, err := f.keyed.List(ctx, tenant, environmenttemplates.ListQuery{Limit: 1})
	if err != nil || reverse.Templates[0].ID != ids[3] {
		t.Fatal(reverse, err)
	}
	if _, err := f.keyed.List(ctx, tenant, environmenttemplates.ListQuery{Limit: environmenttemplates.MaxListLimit + 1}); !errors.Is(err, environmenttemplates.ErrInvalidInput) {
		t.Fatal("oversized page", err)
	}
	cleared := f.update(t, tenant, created.ID, environmenttemplates.Input{SetName: true, SetNetwork: true, NetworkAccess: "enabled"})
	if cleared.Name != nil || cleared.NetworkAccess != "enabled" {
		t.Fatal(cleared)
	}
	if id, err := f.service.Delete(ctx, environmenttemplates.DeleteCommand{TenantID: tenant, TemplateID: created.ID}); err != nil || id != created.ID {
		t.Fatal(id, err)
	}
	if _, err := f.keyed.Get(ctx, tenant, created.ID); !errors.Is(err, environmenttemplates.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestEmptyUpdateTouchesTimeWithoutKeyOrContents(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	tenant := uuid.NewString()
	original := f.create(t, tenant, environmenttemplates.Input{
		Name:  ptr("Retained template"),
		Files: []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/input.txt", Data: []byte("file-canary")}},
		Setup: environmentconfig.Setup{Env: map[string]string{"PRIVATE_SETUP": "env-canary"}, Commands: []environmentconfig.SetupCommand{{Command: "echo setup-canary"}}},
	})
	row := func() []byte {
		t.Helper()
		var contents []byte
		if err := f.pool.QueryRow(t.Context(), "SELECT to_jsonb(t) - 'updated_at' FROM environment_templates t WHERE id=$1", original.ID).Scan(&contents); err != nil {
			t.Fatal(err)
		}
		return contents
	}
	before := row()
	if _, err := f.keyless.Update(t.Context(), uuid.NewString(), original.ID, environmenttemplates.Input{}); !errors.Is(err, environmenttemplates.ErrNotFound) {
		t.Fatal("foreign empty update was admitted", err)
	}
	updated, err := f.keyless.Update(t.Context(), tenant, original.ID, environmenttemplates.Input{})
	if err != nil || !updated.UpdatedAt.After(original.UpdatedAt) {
		t.Fatal("empty update did not advance the timestamp without a key", err)
	}
	original.UpdatedAt = updated.UpdatedAt
	if !reflect.DeepEqual(updated, original) || !bytes.Equal(before, row()) {
		t.Fatal("empty update changed metadata, ciphertext or ownership")
	}
	if retained := f.resolve(t, tenant, original.ID); retained.Setup.Env["PRIVATE_SETUP"] != "env-canary" || len(retained.Setup.Commands) != 1 || len(retained.Files) != 1 {
		t.Fatal("empty update invalidated confidential configuration")
	}
}

func TestNetworkPolicyRoundTripAndReplacement(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	domains := []string{"Example.com", "api.example.com", "Example.com"}
	check := func(value environmenttemplates.Template, err error) {
		t.Helper()
		if err != nil || value.NetworkAccess != "restricted" || !reflect.DeepEqual(value.AllowedDomains, domains) {
			t.Fatalf("network policy lost: %#v, %v", value, err)
		}
	}
	created := f.create(t, tenant, environmenttemplates.Input{SetNetwork: true, NetworkAccess: "restricted", AllowedDomains: domains})
	check(created, nil)
	check(f.keyless.Get(ctx, tenant, created.ID))
	check(f.resolve(t, tenant, created.ID).Template, nil)
	page, err := f.keyless.List(ctx, tenant, environmenttemplates.ListQuery{Limit: 1, Ascending: true})
	if err != nil || len(page.Templates) != 1 {
		t.Fatal(page, err)
	}
	check(page.Templates[0], nil)
	check(f.update(t, tenant, created.ID, environmenttemplates.Input{SetName: true, Name: ptr("renamed")}), nil)
	if _, err := f.keyed.Resolve(ctx, foreign, created.ID); !errors.Is(err, environmenttemplates.ErrNotFound) {
		t.Fatal("foreign policy resolution", err)
	}
	for _, in := range []environmenttemplates.Input{
		{SetNetwork: true, NetworkAccess: "enabled", AllowedDomains: domains},
		{SetNetwork: true, NetworkAccess: "restricted"},
		{SetNetwork: true, NetworkAccess: "restricted", AllowedDomains: []string{"*.example.com"}},
	} {
		if _, err := f.service.Update(ctx, environmenttemplates.UpdateCommand{TenantID: tenant, TemplateID: created.ID, Input: in}); !errors.Is(err, environmenttemplates.ErrInvalidInput) {
			t.Fatal("invalid policy replacement", err)
		}
		check(f.keyless.Get(ctx, tenant, created.ID))
	}
	domains = []string{"other.example.com"}
	check(f.update(t, tenant, created.ID, environmenttemplates.Input{SetNetwork: true, NetworkAccess: "restricted", AllowedDomains: domains}), nil)
	for _, access := range []string{"disabled", "enabled"} {
		value := f.update(t, tenant, created.ID, environmenttemplates.Input{SetNetwork: true, NetworkAccess: access})
		if value.NetworkAccess != access || value.AllowedDomains == nil || len(value.AllowedDomains) != 0 {
			t.Fatal("policy reset", value)
		}
	}
}

func TestSetupSealedAndReplacedPerField(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	tenant, foreign := uuid.NewString(), uuid.NewString()
	setup := environmentconfig.Setup{Env: map[string]string{"SECRET": "template-env-canary"}, Commands: []environmentconfig.SetupCommand{{Command: "printf template-command-canary > result"}}, Packages: v1.EnvironmentPackages{NPM: []string{"is-number@7.0.0"}}}
	template := f.create(t, tenant, environmenttemplates.Input{Setup: setup, SetEnv: true, SetCommands: true, SetPackages: true})
	public, err := f.keyless.Get(t.Context(), tenant, template.ID)
	if err != nil || !reflect.DeepEqual(public.Packages.NPM, setup.Packages.NPM) {
		t.Fatal("public metadata requires plaintext or key", err)
	}
	resolved := f.resolve(t, tenant, template.ID)
	if !reflect.DeepEqual(resolved.Setup.Env, setup.Env) || !reflect.DeepEqual(resolved.Setup.Commands, setup.Commands) || !reflect.DeepEqual(resolved.Setup.Packages.NPM, setup.Packages.NPM) {
		t.Fatal("confidential configuration resolution", resolved.Setup)
	}
	if _, err := f.keyed.Resolve(t.Context(), foreign, template.ID); !errors.Is(err, environmenttemplates.ErrNotFound) {
		t.Fatal("foreign resolution", err)
	}
	f.requireSealed(t, template.ID, "env_contents", "template-env-canary")
	f.requireSealed(t, template.ID, "setup_contents", "template-command-canary")
	f.update(t, tenant, template.ID, environmenttemplates.Input{SetName: true, Name: ptr("renamed")})
	if retained := f.resolve(t, tenant, template.ID); !reflect.DeepEqual(retained.Setup, resolved.Setup) {
		t.Fatal("name update changed setup")
	}
	f.update(t, tenant, template.ID, environmenttemplates.Input{SetEnv: true})
	if cleared := f.resolve(t, tenant, template.ID); len(cleared.Setup.Env) != 0 || !reflect.DeepEqual(cleared.Setup.Commands, setup.Commands) {
		t.Fatal("field replacement lost unrelated values")
	}
}

func TestInitialFilesSealedWithNoncanonicalIDs(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	tenant, foreign := uuid.NewString(), uuid.NewString()
	canary := []byte("private-initial-file-canary\x00\xff")
	files := []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/a/data", Data: canary}, {Type: "file_id", Path: "/workspace/b", FileID: "file-" + uuid.NewString()}}
	template := f.create(t, tenant, environmenttemplates.Input{SetFiles: true, Files: files})
	public, err := f.keyless.Get(t.Context(), tenant, template.ID)
	if err != nil || len(public.Files) != 2 || *public.Files[0].SizeBytes != int64(len(canary)) {
		t.Fatal("public read depends on the key", err)
	}
	if _, err := f.keyed.Resolve(t.Context(), foreign, template.ID); !errors.Is(err, environmenttemplates.ErrNotFound) {
		t.Fatal("foreign template resolved", err)
	}
	f.update(t, strings.ToUpper(tenant), strings.ToUpper(template.ID), environmenttemplates.Input{SetFiles: true, Files: files})
	if resolved := f.resolve(t, strings.ToUpper(tenant), strings.ToUpper(template.ID)); !reflect.DeepEqual(resolved.Files, files) {
		t.Fatal("noncanonical resolution")
	}
	f.requireSealed(t, template.ID, "file_contents", "private-initial-file-canary")
	f.update(t, tenant, template.ID, environmenttemplates.Input{SetFiles: true})
	if cleared := f.resolve(t, tenant, template.ID); len(cleared.Files) != 0 || len(cleared.Template.Files) != 0 {
		t.Fatal("files were not cleared")
	}
}

func TestSkillsAndPluginsSealedAndPreserved(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	ctx := t.Context()
	setup := environmentconfig.Setup{
		Skills:                []environmentconfig.Skill{{Metadata: environmentconfig.SkillMetadata{Type: "inline", Name: "proof", Description: "A proof."}, Archive: archive(t, map[string]string{"proof/SKILL.md": "---\nname: proof\ndescription: A proof.\n---\nskill-private-canary"})}},
		Plugins:               []environmentconfig.Plugin{{Metadata: agentplugin.Metadata{Type: "inline", Name: "plugin-proof", Description: "A proof."}, Archive: archive(t, map[string]string{"proof/.codex-plugin/plugin.json": `{"name":"plugin-proof","description":"A proof.","skills":"./skills"}`, "proof/skills/proof/SKILL.md": "---\nname: proof\ndescription: A proof.\n---\nplugin-private-canary"})}},
		CapabilityDirectories: []string{"/workspace/generated"},
	}
	tenant, foreign := uuid.NewString(), uuid.NewString()
	template := f.create(t, tenant, environmenttemplates.Input{SetSkills: true, SetPlugins: true, SetDirectories: true, Setup: setup})
	public, err := f.keyless.Get(ctx, tenant, template.ID)
	if err != nil || len(public.Skills) != 1 || len(public.Plugins) != 1 || !reflect.DeepEqual(public.CapabilityDirectories, setup.CapabilityDirectories) {
		t.Fatal("safe metadata without the key", err)
	}
	if page, err := f.keyless.List(ctx, tenant, environmenttemplates.ListQuery{Limit: 20}); err != nil || len(page.Templates) != 1 || len(page.Templates[0].Plugins) != 1 {
		t.Fatal("list", err)
	}
	f.requireSealed(t, template.ID, "skill_contents", "skill-private-canary")
	f.requireSealed(t, template.ID, "plugin_contents", "plugin-private-canary")
	if resolved := f.resolve(t, tenant, template.ID); !reflect.DeepEqual(resolved.Setup.Skills, setup.Skills) || !reflect.DeepEqual(resolved.Setup.Plugins, setup.Plugins) {
		t.Fatal("resolution")
	}
	for _, call := range []func() error{
		func() error { _, e := f.keyed.Get(ctx, foreign, template.ID); return e },
		func() error { _, e := f.keyed.Resolve(ctx, foreign, template.ID); return e },
		func() error {
			_, e := f.keyed.Update(ctx, foreign, template.ID, environmenttemplates.Input{SetPlugins: true})
			return e
		},
	} {
		if err := call(); !errors.Is(err, environmenttemplates.ErrNotFound) {
			t.Fatal("tenant isolation", err)
		}
	}
	f.update(t, tenant, template.ID, environmenttemplates.Input{SetName: true, Name: ptr("renamed")})
	if preserved := f.resolve(t, tenant, template.ID); !reflect.DeepEqual(preserved.Setup.Skills, setup.Skills) || !reflect.DeepEqual(preserved.Setup.Plugins, setup.Plugins) || !reflect.DeepEqual(preserved.Setup.CapabilityDirectories, setup.CapabilityDirectories) {
		t.Fatal("unrelated update changed installation")
	}
	f.update(t, tenant, template.ID, environmenttemplates.Input{SetSkills: true, SetPlugins: true, SetDirectories: true})
	if cleared := f.resolve(t, tenant, template.ID); len(cleared.Setup.Skills) != 0 || len(cleared.Setup.Plugins) != 0 || len(cleared.Template.CapabilityDirectories) != 0 {
		t.Fatal("clear")
	}
}

// A Template saves a Skill reference as the caller selected it. Session
// creation, not the Template, resolves the selector to a version, so deleting
// or re-versioning the Skill never changes the Template.
func TestSkillReferenceKeepsSelector(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	tenant := uuid.NewString()
	reference := environmentconfig.Skill{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: "skill_" + uuid.NewString()}}
	template := f.create(t, tenant, environmenttemplates.Input{SetSkills: true, Setup: environmentconfig.Setup{Skills: []environmentconfig.Skill{reference}}})
	resolved := f.resolve(t, tenant, template.ID)
	if len(resolved.Setup.Skills) != 1 || resolved.Setup.Skills[0].Metadata != reference.Metadata || len(resolved.Setup.Skills[0].Archive) != 0 || resolved.Template.Skills[0].Version != "" {
		t.Fatal("template resolved a mutable selector", resolved.Setup.Skills)
	}
}

func TestUnstorableTextIsRejected(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	tenant := uuid.NewString()
	if _, err := f.service.Create(t.Context(), environmenttemplates.CreateCommand{TenantID: tenant, Input: environmenttemplates.Input{Name: ptr("a\x00b")}}); !errors.Is(err, textvalue.ErrUnstorable) {
		t.Fatal("create", err)
	}
	template := f.create(t, tenant, environmenttemplates.Input{})
	if _, err := f.service.Update(t.Context(), environmenttemplates.UpdateCommand{TenantID: tenant, TemplateID: template.ID, Input: environmenttemplates.Input{SetName: true, Name: ptr("a\x00b")}}); !errors.Is(err, textvalue.ErrUnstorable) {
		t.Fatal("update", err)
	}
}

func TestMissingKeyIsUnavailable(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	tenant := uuid.NewString()
	setup := environmenttemplates.Input{Setup: environmentconfig.Setup{Env: map[string]string{"PRIVATE_SETUP": "env-canary"}}}
	files := environmenttemplates.Input{SetFiles: true, Files: []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/input.txt", Data: []byte("file-canary")}}}
	for _, in := range []environmenttemplates.Input{setup, files} {
		if _, err := f.keyless.Create(t.Context(), tenant, in); !errors.Is(err, credentialcrypto.ErrUnavailable) {
			t.Fatal("confidential create without a key", err)
		}
		template := f.create(t, tenant, in)
		if _, err := f.keyless.Resolve(t.Context(), tenant, template.ID); !errors.Is(err, credentialcrypto.ErrUnavailable) {
			t.Fatal("confidential resolve without a key", err)
		}
	}
}

// Stored package metadata naming the removed system manager is rejected on
// every read instead of being silently dropped.
func TestStoredSystemPackagesRejected(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	ctx := t.Context()
	tenant := uuid.NewString()
	template := f.create(t, tenant, environmenttemplates.Input{})
	store := func(packages string) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, "UPDATE environment_templates SET packages=$1 WHERE id=$2", []byte(packages), template.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{`null`, `[]`, `["jq"]`} {
		store(`{"npm":[],"python":[],"system":` + value + `}`)
		if _, err := f.keyless.Get(ctx, tenant, template.ID); !errors.Is(err, environmenttemplates.ErrInvalidInput) {
			t.Fatal("get silently ignored removed system packages", value, err)
		}
		if _, err := f.keyless.List(ctx, tenant, environmenttemplates.ListQuery{Limit: 1}); !errors.Is(err, environmenttemplates.ErrInvalidInput) {
			t.Fatal("list silently ignored removed system packages", value, err)
		}
		if _, err := f.keyed.Resolve(ctx, tenant, template.ID); !errors.Is(err, environmenttemplates.ErrInvalidInput) {
			t.Fatal("resolve silently ignored removed system packages", value, err)
		}
	}
	store(`{"npm":["semver"],"python":["packaging"]}`)
	if got, err := f.keyless.Get(ctx, tenant, template.ID); err != nil || !reflect.DeepEqual(got.Packages, v1.EnvironmentPackages{NPM: []string{"semver"}, Python: []string{"packaging"}}) {
		t.Fatal("supported stored package managers rejected", got.Packages, err)
	}
}

func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		file, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
