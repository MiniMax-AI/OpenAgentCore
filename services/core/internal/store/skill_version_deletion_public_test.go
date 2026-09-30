package store_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

// TestSkillVersionDeletionHTTPPostgres covers skills.versions.delete over real
// HTTP and PostgreSQL: sole-version deletion (V1), default rejection while
// other versions remain (V2), nondefault latest deletion (V3) and tenant
// isolation (V4).
func TestSkillVersionDeletionHTTPPostgres(t *testing.T) {
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{64}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	owner, foreign, ownerTenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "skill-owner", TokenSHA256: runtimedevice.HashCredential(owner), TenantID: ownerTenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "skill-foreign", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(s, auth, "codex", api.WithSkills(s))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := pathIDClient{t: t, server: server}
	object := func(raw string) map[string]any {
		t.Helper()
		var value map[string]any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(raw, err)
		}
		return value
	}
	expect := func(token, method, path string, status int) string {
		t.Helper()
		got, raw := client.do(token, method, path, "", nil)
		if got != status {
			t.Fatalf("%s %s: %d %s", method, path, got, raw)
		}
		return raw
	}
	missing := expect(owner, http.MethodDelete, "/v1/skills/skill_"+uuid.NewString()+"/versions/1", http.StatusNotFound)

	sole, err := s.CreateSkill(t.Context(), ownerTenant, store.SkillArchive(t, "sole-http"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteSkill(t.Context(), ownerTenant, sole.ID) })
	version := object(expect(owner, http.MethodGet, "/v1/skills/"+sole.ID+"/versions/1", http.StatusOK))
	path := "/v1/skills/" + sole.ID
	// V4: a foreign tenant receives the missing-Skill response and changes nothing.
	if raw := expect(foreign, http.MethodDelete, path+"/versions/1", http.StatusNotFound); raw != missing {
		t.Fatal("foreign response differs from missing", raw, missing)
	}
	expect(owner, http.MethodDelete, path+"/versions/2", http.StatusNotFound)
	expect(owner, http.MethodGet, path, http.StatusOK)

	// V1: the observed official body, then the Skill is gone from every read.
	deleted := object(expect(owner, http.MethodDelete, path+"/versions/1", http.StatusOK))
	if want := map[string]any{"id": version["id"], "object": "skill.version.deleted", "deleted": true, "version": "1"}; !reflect.DeepEqual(deleted, want) {
		t.Fatal("sole-version deletion body", deleted)
	}
	for _, suffix := range []string{"", "/content", "/versions", "/versions/1", "/versions/1/content"} {
		expect(owner, http.MethodGet, path+suffix, http.StatusNotFound)
	}
	if raw := expect(owner, http.MethodGet, "/v1/skills", http.StatusOK); strings.Contains(raw, sole.ID) {
		t.Fatal("deleted Skill listed", raw)
	}
	expect(owner, http.MethodDelete, path+"/versions/1", http.StatusNotFound)
	expect(owner, http.MethodDelete, path, http.StatusNotFound)

	// V2 and V3 on a Skill with two versions, then V1 on the reduced Skill.
	pair, err := s.CreateSkill(t.Context(), ownerTenant, store.SkillArchive(t, "pair-one"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteSkill(t.Context(), ownerTenant, pair.ID) })
	if _, err = s.CreateSkillVersion(t.Context(), ownerTenant, pair.ID, store.SkillArchive(t, "pair-two"), false); err != nil {
		t.Fatal(err)
	}
	path = "/v1/skills/" + pair.ID
	before := expect(owner, http.MethodGet, path+"/versions", http.StatusOK)
	rejected := object(expect(owner, http.MethodDelete, path+"/versions/1", http.StatusBadRequest))
	if want := map[string]any{"error": map[string]any{"type": "invalid_request_error", "code": "invalid_value", "param": "version", "message": "Cannot delete the default skill version."}}; !reflect.DeepEqual(rejected, want) {
		t.Fatal("default deletion with another version", rejected)
	}
	expect(foreign, http.MethodDelete, path+"/versions/2", http.StatusNotFound)
	if after := expect(owner, http.MethodGet, path+"/versions", http.StatusOK); after != before {
		t.Fatal("rejected deletions changed versions", after)
	}
	if latest := object(expect(owner, http.MethodDelete, path+"/versions/2", http.StatusOK)); latest["version"] != "2" || latest["deleted"] != true {
		t.Fatal("latest deletion", latest)
	}
	if parent := object(expect(owner, http.MethodGet, path, http.StatusOK)); parent["default_version"] != "1" || parent["latest_version"] != "1" {
		t.Fatal("latest pointer fallback", parent)
	}
	if reduced := object(expect(owner, http.MethodDelete, path+"/versions/1", http.StatusOK)); reduced["version"] != "1" || reduced["object"] != "skill.version.deleted" {
		t.Fatal("reduced sole-version deletion", reduced)
	}
	expect(owner, http.MethodGet, path, http.StatusNotFound)
}
