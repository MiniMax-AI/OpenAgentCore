package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type adminReadFixture struct {
	ResourceStore
	seenTenant                   string
	administrative, impersonated bool
}

func (s *adminReadFixture) ListAgents(ctx context.Context, tenant, after string, limit int, ascending bool) (store.AgentPage, error) {
	s.seenTenant = tenant
	_, s.administrative = adminaudit.FromContext(ctx)
	s.impersonated = ctx.Value(principalContextKey{}) != nil
	return store.AgentPage{Agents: []store.SavedAgent{}}, nil
}
func (s *adminReadFixture) DeleteAgent(ctx context.Context, tenant, id string) (string, error) {
	s.seenTenant = tenant
	_, s.administrative = adminaudit.FromContext(ctx)
	s.impersonated = ctx.Value(principalContextKey{}) != nil
	return id, nil
}
func TestAdminResourcesHaveExplicitTargetWithoutCallerImpersonation(t *testing.T) {
	key := callerBinding()
	auth, err := NewAuthenticator([]APIKey{key})
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := NewDeploymentAuthenticator([]string{device.HashCredential("admin")})
	resources := &adminReadFixture{}
	h, err := NewHandler(resources, auth, "codex", WithProjectAPIKeys(&projectKeyStoreFixture{}, admin))
	if err != nil {
		t.Fatal(err)
	}
	base := "/core/v1/admin/api-keys/static:" + key.TokenSHA256
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"GET", base + "/agents", 200}, {"DELETE", base + "/agents/known", 200},
		{"POST", base + "/agents", 405}, {"POST", base + "/sessions", 405},
		{"POST", base + "/sessions/known/events", 404}, {"GET", base + "/sessions/known/events", 404},
		{"GET", base + "/files/known/content", 404}, {"POST", base + "/vaults/v/credentials", 405},
	} {
		body := ""
		if test.method == http.MethodPost {
			body = `{}`
		}
		w := projectKeyHTTP(h, test.method, test.path, "admin", body)
		if w.Code != test.status {
			t.Errorf("%s %s: %d %s", test.method, test.path, w.Code, w.Body)
		}
	}
	if resources.seenTenant != key.TenantID || !resources.administrative || resources.impersonated {
		t.Fatal("administrator target became a caller or lost audit scope")
	}
	for _, path := range []string{base + "/agents", "/core/v1/admin/not-an-operation"} {
		for _, token := range []string{"caller", "issued-project-key", ""} {
			if w := projectKeyHTTP(h, "GET", path, token, ""); w.Code != 401 {
				t.Fatalf("unauthorized management path returned %d", w.Code)
			}
		}
	}
	for _, path := range []string{"/core/v1/project-api-keys/" + key.TokenSHA256, "/core/v1/resource-owners", "/core/v1/write-operations"} {
		if w := projectKeyHTTP(h, "GET", path, "admin", ""); w.Code != 404 {
			t.Fatal("retired route still served", path, w.Code)
		}
	}
}

type summaryFixture struct {
	AdminManagementStore
	tenant string
	filter store.AdminSummaryFilter
}

func (s *summaryFixture) ReadAdminSummary(_ context.Context, tenant string, filter store.AdminSummaryFilter, visit func(store.Session) error) (store.AdminAssetCounts, error) {
	s.tenant, s.filter = tenant, filter
	for i, usage := range []json.RawMessage{nil, json.RawMessage(`{"input_tokens":3,"output_tokens":5,"total_tokens":8,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":1}}`)} {
		session := store.Session{ID: "session", TenantID: tenant, Configuration: json.RawMessage(`{"agent":{"id":"agent","model":"model","tools":[]},"environment":{"type":"none"}}`), CreatedAt: time.Unix(100+int64(i), 0), Usage: usage}
		if i == 0 {
			session.LastTurn = &store.Turn{Status: store.TurnInProgress, CreatedAt: time.Unix(110, 0)}
		}
		if err := visit(session); err != nil {
			return store.AdminAssetCounts{}, err
		}
	}
	return store.AdminAssetCounts{Agents: 4, Skills: 2}, nil
}
func TestAdminSummaryUsesPublicStateAndNullUsageCoverage(t *testing.T) {
	key := callerBinding()
	auth, _ := NewAuthenticator([]APIKey{key})
	admin, _ := NewDeploymentAuthenticator([]string{device.HashCredential("admin")})
	fixture := &summaryFixture{}
	h, err := NewHandler(&recordingStore{}, auth, "codex", WithProjectAPIKeys(&projectKeyStoreFixture{}, admin), WithAdminManagement(fixture))
	if err != nil {
		t.Fatal(err)
	}
	base := "/core/v1/admin/summary?key_id=static:" + key.TokenSHA256 + "&created_after=1970-01-01T00:00:00Z&created_before=2030-01-01T00:00:00Z"
	for _, group := range []string{"key", "agent"} {
		w := projectKeyHTTP(h, http.MethodGet, base+"&group_by="+group, "admin", "")
		var response AdminSummaryResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Data) != 1 {
			t.Fatalf("summary %d %s", w.Code, w.Body)
		}
		row := response.Data[0]
		if row.Sessions.Total != 2 || row.Sessions.InProgress != 1 || row.Sessions.Idle != 1 || row.Usage.TotalTokens != 8 || row.Usage.InputTokensDetails.CachedTokens != 2 || row.Coverage.MeasuredSessions != 1 || row.Coverage.Ratio == nil || *row.Coverage.Ratio != .5 || row.LastActiveAt == nil || *row.LastActiveAt != 110 {
			t.Fatalf("incorrect aggregate %+v", row)
		}
		if group == "key" && (row.Assets == nil || row.Assets.Agents != 4 || row.AgentID != nil) {
			t.Fatal("key assets omitted")
		}
		if group == "agent" && (row.Assets != nil || row.AgentID == nil || *row.AgentID != "agent") {
			t.Fatal("agent groups incorrect")
		}
	}
	if fixture.tenant != key.TenantID || fixture.filter.CreatedAfter == nil || fixture.filter.CreatedBefore == nil {
		t.Fatal("scope/filter lost")
	}
	for _, query := range []string{"&group_by=unknown", "&limit=0", "&after=another", "&created_after=duplicate"} {
		if w := projectKeyHTTP(h, "GET", base+query, "admin", ""); w.Code != 400 {
			t.Errorf("invalid %s: %d", query, w.Code)
		}
	}
	if w := projectKeyHTTP(h, "GET", strings.Replace(base, "2030-01-01T00:00:00Z", "1960-01-01T00:00:00Z", 1), "admin", ""); w.Code != 400 {
		t.Fatal("reversed time range accepted")
	}
}
