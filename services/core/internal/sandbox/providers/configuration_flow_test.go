package providers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type regionalConfiguration struct {
	Zone string `json:"zone"`
}

func (regionalConfiguration) HasCredential() bool      { return false }
func (regionalConfiguration) ReplacesCredential() bool { return false }

type regionalCodec struct{}

func (regionalCodec) Requirements() sandbox.ConfigurationRequirements {
	return sandbox.ConfigurationRequirements{Credential: sandbox.NotRequired, PublicOrigin: sandbox.NotRequired, Discovery: providercontract.Support{State: providercontract.Unsupported, Reason: "node_configuration_has_no_catalog"}}
}
func (regionalCodec) WithCredential(sandbox.Configuration, sandbox.Configuration) (sandbox.Configuration, error) {
	return nil, &providercontract.UnsupportedError{Operation: "WithCredential", Reason: "credentials_not_required"}
}
func (regionalCodec) DecodeInput(raw, secret json.RawMessage) (sandbox.Configuration, error) {
	var c regionalConfiguration
	if len(secret) > 0 || sandbox.DecodeConfigurationObject(raw, &c, "zone") != nil || c.Zone == "" {
		return nil, sandbox.ErrInvalid
	}
	return c, nil
}
func (a regionalCodec) Decode(r sandbox.ConfigurationRecord) (sandbox.Configuration, error) {
	if len(r.Secret) > 0 || sandbox.DecodeConfigurationObject(r.Metadata, &struct{}{}) != nil {
		return nil, sandbox.ErrInvalid
	}
	return a.DecodeInput(r.Public, nil)
}
func (regionalCodec) Encode(c sandbox.Configuration) (sandbox.ConfigurationRecord, error) {
	v, ok := c.(regionalConfiguration)
	if !ok || v.Zone == "" {
		return sandbox.ConfigurationRecord{}, sandbox.ErrInvalid
	}
	raw, _ := json.Marshal(v)
	return sandbox.ConfigurationRecord{Public: raw, Metadata: json.RawMessage(`{}`)}, nil
}
func (a regionalCodec) Normalize(s sandbox.Selection) (sandbox.Selection, error) {
	_, err := a.Encode(s.Configuration)
	return s, err
}
func (a regionalCodec) ResolveChange(next, previous sandbox.Selection) (sandbox.Selection, error) {
	return a.Normalize(next)
}
func (a regionalCodec) Equal(x, y sandbox.Configuration) (bool, error) {
	_, err := a.Encode(x)
	if err != nil {
		return false, err
	}
	_, err = a.Encode(y)
	return x == y, err
}
func (regionalCodec) DiscoverConfiguration(context.Context, sandbox.ConfigurationDiscoveryInput, sandbox.ProcessPaths) (json.RawMessage, error) {
	return nil, &providercontract.UnsupportedError{Operation: "DiscoverConfiguration", Reason: "node_configuration_has_no_catalog"}
}

// A registered native configuration reaches the ordinary API and Store without
// adding its fields or kind to either Core package.
func TestAdditionalConfigurationProviderUsesCommonAPIAndStore(t *testing.T) {
	dsn := os.Getenv("OAC_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "oac_") || !strings.HasSuffix(cfg.ConnConfig.Database, "_tests") {
		t.Fatal("dedicated test database required")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "oac_provider_" + uuid.NewString()[:8] + "_tests"
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP DATABASE "+quoted+" WITH (FORCE)")
	cfg.ConnConfig.Database = name
	db := sql.OpenDB(stdlib.GetConnector(*cfg.ConnConfig))
	migration, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = migration.Up(t.Context())
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	kind := "regional-fixture"
	adapter, err := providers.Lookup("docker")
	if err != nil {
		t.Fatal(err)
	}
	adapter.Configuration = regionalCodec{}
	providers.RegisterFixture(t, kind, adapter)
	s := store.New(pool)
	lease, err := s.AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	w := lease.Store()
	installation := uuid.NewString()
	if err = w.ClaimWebSandboxDeployment(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	auth, err := api.NewDeploymentAuthenticator([]string{device.HashCredential("fixture-admin")})
	if err != nil {
		t.Fatal(err)
	}
	projectAuth, err := api.NewDatabaseAuthenticator(s)
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(s, projectAuth, "codex", api.WithSandboxManager(s, auth), api.WithSandboxDeploymentSetup(func(ctx context.Context, in store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error) {
		return w.InitializeSandboxDeployment(ctx, installation, in)
	}))
	if err != nil {
		t.Fatal(err)
	}
	runtime := sandbox.RuntimeRelease{SourceCommit: strings.Repeat("a", 40), ImageID: "sha256:" + strings.Repeat("b", 64), ImageManifestDigest: "sha256:" + strings.Repeat("c", 64), MicrosandboxRef: "oac-runtime@sha256:" + strings.Repeat("d", 64), RuntimeSHA256: strings.Repeat("e", 64), FirmwareSHA256: strings.Repeat("f", 64)}
	body, _ := json.Marshal(map[string]any{"provider": kind, "expected_generation": 0, "resources": sandbox.Resources{CPUs: 2, MemoryMiB: 2048}, "runtime": runtime, "configuration": map[string]string{"zone": "west"}})
	request := httptest.NewRequest("POST", "/core/v1/sandbox/deployment", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer fixture-admin")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"zone":"west"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	reopened := store.New(pool)
	saved, err := reopened.GetSandboxSetup(t.Context())
	if err != nil || saved.Configuration.(regionalConfiguration).Zone != "west" {
		t.Fatal("configuration did not roundtrip", err)
	}
	var raw []byte
	if err = pool.QueryRow(t.Context(), "SELECT provider_config FROM runtime_deployment").Scan(&raw); err != nil || !strings.Contains(string(raw), `"zone": "west"`) {
		t.Fatal("native fields not persisted", err)
	}
}
