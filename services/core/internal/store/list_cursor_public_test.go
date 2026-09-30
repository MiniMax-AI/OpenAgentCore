package store_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

// cursorFixture is one tenant's resources for the list cursor matrix. Each list
// has a second parent in the same tenant, so other-parent cursors are real IDs.
type cursorFixture struct {
	agent, template, vault, otherVault, credential, otherCredential string
	session, turn, item, otherSession, otherItem                    string
	artifactSession, artifactTurn, artifact, otherArtifact          string
	subSession, rootTurn, rootItem, otherSubagent, otherChildTurn   string
	child, childTurn, childItem, laterChildTurn, laterChildItem     string
	sibling, siblingTurn, siblingItem                               string
	skill, version, laterVersion, deletedVersion, otherSkillVersion string
	file                                                            string
}

func seedCursorFixture(t *testing.T, s *store.Store, leased execution.Owner, skillService *skills.Service, sessionService *sessions.Service, client pathIDClient, token, tenant, label string) cursorFixture {
	t.Helper()
	ctx := t.Context()
	var f cursorFixture
	// Each list has at least two resources, so valid cursors page between them.
	f.agent = client.created(token, "/v1/agents", `{"model":"cursor-model"}`)
	client.created(token, "/v1/agents", `{"model":"cursor-model"}`)
	f.template = client.created(token, "/v1/agents/environments/templates", `{"name":"cursor-template"}`)
	client.created(token, "/v1/agents/environments/templates", `{"name":"cursor-template-2"}`)
	f.vault = client.created(token, "/v1/vaults", `{"name":"cursor-vault"}`)
	f.otherVault = client.created(token, "/v1/vaults", `{"name":"cursor-other-vault"}`)
	credential := `{"name":"cursor","auth":{"type":"static_bearer","mcp_server_url":"https://mcp.example/mcp","token":"cursor-token"}}`
	f.credential = client.created(token, "/v1/vaults/"+f.vault+"/credentials", credential)
	client.created(token, "/v1/vaults/"+f.vault+"/credentials", credential)
	f.otherCredential = client.created(token, "/v1/vaults/"+f.otherVault+"/credentials", credential)

	// Queued Turns and user Items exist without a daemon.
	first := func(path string) string {
		t.Helper()
		status, raw := client.do(token, http.MethodGet, path+"?order=asc", "", nil)
		var page struct{ Data []struct{ ID string } }
		if status != http.StatusOK || json.Unmarshal([]byte(raw), &page) != nil || len(page.Data) == 0 {
			t.Fatalf("fixture %s: %d %s", path, status, raw)
		}
		return page.Data[0].ID
	}
	// Both Sessions use the saved Agent, so the Session list can page them by agent_id.
	newSession := `{"agent_id":"` + f.agent + `","environment":{"type":"none"},"input":"Keep this Session."}`
	f.session = client.created(token, "/v1/agents/sessions", newSession)
	f.turn = first("/v1/agents/sessions/" + f.session + "/turns")
	f.item = first("/v1/agents/sessions/" + f.session + "/items")
	if _, err := s.TransitionTurn(ctx, tenant, f.session, f.turn, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnCancelled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitMessage(ctx, tenant, f.session, label+"-second", json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"second"}]}]}`)); err != nil {
		t.Fatal(err)
	}
	f.otherSession = client.created(token, "/v1/agents/sessions", newSession)
	f.otherItem = first("/v1/agents/sessions/" + f.otherSession + "/items")

	artifactSession, environment := hostedArtifactSession(t, s, tenant, label+"-artifacts")
	f.artifactSession = artifactSession
	f.artifactTurn = completeArtifactTurn(t, s, sessionService, tenant, artifactSession, environment, label+"-artifact-turn", map[string]string{"a.txt": "alpha", "c.txt": "charlie"})
	f.artifact = first("/v1/agents/sessions/" + artifactSession + "/artifacts")
	otherArtifactSession, otherEnvironment := hostedArtifactSession(t, s, tenant, label+"-other-artifacts")
	completeArtifactTurn(t, s, sessionService, tenant, otherArtifactSession, otherEnvironment, label+"-other-artifact-turn", map[string]string{"b.txt": "bravo"})
	f.otherArtifact = first("/v1/agents/sessions/" + otherArtifactSession + "/artifacts")

	// Subagent history is seeded through the execution lease, as a daemon would.
	seedSubagents := func(key string) (session, rootTurn string) {
		t.Helper()
		created, err := s.CreateSession(ctx, tenant, sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: key,
			Configuration: json.RawMessage(`{"agent":{"id":"agent_root","model":"cursor-model","multi_agent":{"enabled":true,"max_concurrent_subagents":4}}}`)})
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := s.SubmitMessage(ctx, tenant, created.ID, key+"-input", json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"delegate"}]}]}`))
		if err != nil {
			t.Fatal(err)
		}
		host, err := sessionService.CreateDevice(ctx, tenant, "cursor "+key, runtimedevice.HashCredential(uuid.NewString()))
		if err != nil {
			t.Fatal(err)
		}
		if err = leased.Sessions.BindSessionDevice(ctx, tenant, created.ID, host.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = leased.Store.TransitionTurn(ctx, tenant, created.ID, receipt.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress}); err != nil {
			t.Fatal(err)
		}
		opened := int64(1700000001000)
		identity := func(child string) sessions.ExecutionEvent {
			return subagentFixture(proto.TypeSubagentIdentity, proto.SubagentIdentityPayload{NativeID: child, ParentNativeID: "root", NativeCreatedAt: 1700000001, ParentTurnID: "native-root", SourceItemID: "spawn-" + child})
		}
		// Distinct creation times keep child-turn before later-child-turn.
		turn := func(child, id string, created int64) sessions.ExecutionEvent {
			return subagentFixture(proto.TypeSubagentTurn, proto.SubagentTurnPayload{NativeID: child, TurnID: id, Status: sessions.TurnInProgress, CreatedAtMS: created, StartedAtMS: &created})
		}
		message := func(child, turn, id string, position int32) sessions.ExecutionEvent {
			text := "answer " + id
			payload, _ := json.Marshal(proto.OutputMessagePayload{ID: id, Status: "completed", Text: &text})
			return subagentFixture(proto.TypeSubagentItem, proto.SubagentItemPayload{NativeID: child, TurnID: turn, ItemID: id, Position: position, Kind: proto.TypeOutputMessage, Payload: payload})
		}
		facts := []sessions.ExecutionEvent{identity("child"), identity("sibling"),
			turn("child", "child-turn", opened), message("child", "child-turn", "child-item", 0), message("child", "child-turn", "child-item-2", 1),
			turn("child", "later-child-turn", opened+1000), message("child", "later-child-turn", "later-child-item", 0),
			turn("sibling", "sibling-turn", opened), message("sibling", "sibling-turn", "sibling-item", 0)}
		if err = leased.Sessions.AppendTurnEvents(ctx, tenant, created.ID, receipt.TurnID, 1, facts); err != nil {
			t.Fatal(err)
		}
		return created.ID, receipt.TurnID
	}
	subagent := func(session, native string) string {
		t.Helper()
		identity, err := s.GetSubagentIdentity(ctx, tenant, session, native)
		if err != nil {
			t.Fatal(err)
		}
		return identity.ID
	}
	f.subSession, f.rootTurn = seedSubagents(label + "-subagents")
	f.rootItem = first("/v1/agents/sessions/" + f.subSession + "/items")
	f.child, f.sibling = subagent(f.subSession, "child"), subagent(f.subSession, "sibling")
	status, raw := client.do(token, http.MethodGet, "/v1/agents/sessions/"+f.subSession+"/subagents/"+f.child+"/turns?order=asc", "", nil)
	var childTurns struct{ Data []struct{ ID string } }
	if status != http.StatusOK || json.Unmarshal([]byte(raw), &childTurns) != nil || len(childTurns.Data) != 2 {
		t.Fatal("fixture child Turns", status, raw)
	}
	f.childTurn, f.laterChildTurn = childTurns.Data[0].ID, childTurns.Data[1].ID
	f.childItem = first("/v1/agents/sessions/" + f.subSession + "/subagents/" + f.child + "/turns/" + f.childTurn + "/items")
	f.laterChildItem = first("/v1/agents/sessions/" + f.subSession + "/subagents/" + f.child + "/turns/" + f.laterChildTurn + "/items")
	f.siblingTurn = first("/v1/agents/sessions/" + f.subSession + "/subagents/" + f.sibling + "/turns")
	f.siblingItem = first("/v1/agents/sessions/" + f.subSession + "/subagents/" + f.sibling + "/items")
	otherSubSession, _ := seedSubagents(label + "-other-subagents")
	f.otherSubagent = subagent(otherSubSession, "child")
	f.otherChildTurn = first("/v1/agents/sessions/" + otherSubSession + "/subagents/" + f.otherSubagent + "/turns")

	skill, err := skillService.CreateSkill(ctx, skills.CreateSkill{TenantID: tenant, Archive: store.SkillArchive(t, label+"-cursor-skill")})
	if err != nil {
		t.Fatal(err)
	}
	f.skill = skill.ID
	skillID := skills.PathID(skill.ID)
	versions, err := skillService.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: skillID, Limit: 10, Ascending: true})
	if err != nil || len(versions.Versions) != 1 {
		t.Fatal("fixture Skill version", versions, err)
	}
	f.version = versions.Versions[0].ID
	later, err := skillService.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: skillID, Archive: store.SkillArchive(t, label+"-cursor-skill-v2")})
	if err != nil {
		t.Fatal(err)
	}
	f.laterVersion = later.ID
	deleted, err := skillService.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: skillID, Archive: store.SkillArchive(t, label+"-cursor-skill-v3")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = skillService.DeleteVersion(ctx, skills.DeleteVersion{TenantID: tenant, SkillID: skillID, Version: 3}); err != nil {
		t.Fatal(err)
	}
	f.deletedVersion = deleted.ID
	otherSkill, err := skillService.CreateSkill(ctx, skills.CreateSkill{TenantID: tenant, Archive: store.SkillArchive(t, label+"-cursor-other-skill")})
	if err != nil {
		t.Fatal(err)
	}
	otherVersions, err := skillService.ListVersions(ctx, skills.ListVersions{TenantID: tenant, SkillID: skills.PathID(otherSkill.ID), Limit: 10, Ascending: true})
	if err != nil || len(otherVersions.Versions) != 1 {
		t.Fatal("fixture other Skill version", otherVersions, err)
	}
	f.otherSkillVersion = otherVersions.Versions[0].ID

	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	if err := form.WriteField("purpose", "user_data"); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("file", "cursor.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("cursor file")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	status, raw = client.do(token, http.MethodPost, "/v1/files", form.FormDataContentType(), upload.Bytes())
	var file struct{ ID string }
	if status != http.StatusOK || json.Unmarshal([]byte(raw), &file) != nil || file.ID == "" {
		t.Fatalf("fixture File: %d %s", status, raw)
	}
	f.file = file.ID
	return f
}

// wireError is the exact serialized error body, including its trailing newline.
func wireError(kind string, code, param *string, message string) string {
	raw, _ := json.Marshal(v1.ErrorResponse{Error: v1.APIError{Message: message, Type: kind, Code: code, Param: param}})
	return string(raw) + "\n"
}

// Unresolved list cursors answer with each family's observed official error
// (ERROR-PROTOCOL-001 rows C1–C5), while Files, Skills, valid cursors and parent
// lookups keep their behavior (K1–K3). A foreign cursor is always byte-identical
// to a missing one, and a foreign or missing parent is 404 before any cursor.
func TestListCursorErrorsPostgres(t *testing.T) {
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{67}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, db := store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
	owner, foreign := uuid.NewString(), uuid.NewString()
	ownerTenant, foreignTenant := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "cursor-owner", TokenSHA256: runtimedevice.HashCredential(owner), TenantID: ownerTenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "cursor-foreign", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: foreignTenant},
	})
	h, err := publicHandler(t, s, db, auth, "codex", storeExecution(t, s))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := pathIDClient{t: t, server: server}
	leased := executionOwner(t, db, s)
	skillService := store.SkillService(t, db.pool, db.cipher)
	_, sessionService, err := fixtureSessions(db)
	if err != nil {
		t.Fatal(err)
	}
	a := seedCursorFixture(t, s, leased, skillService, sessionService, client, owner, ownerTenant, "a")
	b := seedCursorFixture(t, s, leased, skillService, sessionService, client, foreign, foreignTenant, "b")

	text := func(value string) *string { return &value }
	var (
		lookupMissing = wireError("not_found_error", text("not_found_error"), nil, "Resource not found.")
		skillsMissing = wireError("invalid_request_error", nil, nil, "Resource not found.")
		itemCursor    = wireError("invalid_request_error", text("invalid_request_error"), nil, "Invalid session item ID in `after`")
		otherCursor   = wireError("invalid_request_error", text("invalid_request_error"), nil, "Invalid resource ID in `after`")
		artifact      = wireError("invalid_request_error", text("invalid_request_error"), nil, "after is not a valid artifact ID")
		otherSkill    = wireError("invalid_request_error", text("invalid_value"), text("after"), "Skill version cursor does not match this skill.")
	)
	prefix := func(value string) string {
		return wireError("invalid_request_error", text("invalid_value"), text("after"), "Invalid 'after': '"+value+"'. Expected an ID that begins with 'skillver'.")
	}
	list := func(token, path, after string) (int, string) {
		t.Helper()
		query := ""
		if after != "" {
			query = "?" + url.Values{"after": {after}}.Encode()
		}
		return client.do(token, http.MethodGet, path+query, "", nil)
	}
	expect := func(token, path string, status int, want string, cursors ...string) {
		t.Helper()
		for _, cursor := range cursors {
			if got, body := list(token, path, cursor); got != status || body != want {
				t.Errorf("GET %s after=%q = %d %s; want %d %s", path, cursor, got, body, status, want)
			}
		}
	}
	malformed := []string{"not-a-valid-id", "sess_0e6cb352a4b62bb3006ab3f4b12e408196925fb4f852b298b8", "00000000-0000-0000-0000-000000000000", "ffffffff-ffff-ffff-ffff-ffffffffffff", "%", "é"}
	random := uuid.NewString()
	sessions := "/v1/agents/sessions/"
	subagents := sessions + a.subSession + "/subagents/"

	// C1: lookup-family lists answer every unresolved cursor, including malformed,
	// other-type, other-parent and foreign ones, exactly like a missing one.
	for path, cursors := range map[string][]string{
		"/v1/agents":                             {a.session, a.template, b.agent},
		"/v1/agents/sessions":                    {a.agent, a.turn, b.session},
		sessions + a.session + "/turns":          {a.item, a.otherItem, a.rootTurn, a.childTurn, b.turn},
		"/v1/agents/environments/templates":      {a.vault, a.agent, b.template},
		"/v1/vaults":                             {a.credential, a.template, b.vault},
		"/v1/vaults/" + a.vault + "/credentials": {a.otherCredential, a.vault, b.credential},
	} {
		expect(owner, path, http.StatusNotFound, lookupMissing, random)
		expect(owner, path, http.StatusNotFound, lookupMissing, malformed...)
		expect(owner, path, http.StatusNotFound, lookupMissing, cursors...)
	}
	// Tenant B sees tenant A's cursors on its own top-level lists as missing too.
	for _, path := range []string{"/v1/agents", "/v1/agents/sessions", "/v1/agents/environments/templates", "/v1/vaults"} {
		expect(foreign, path, http.StatusNotFound, lookupMissing, a.agent, a.session, a.template, a.vault, "not-a-valid-id")
	}

	// C2: Item lists reject any cursor that is not an Item of their exact scope.
	expect(owner, sessions+a.session+"/items", http.StatusBadRequest, itemCursor,
		append([]string{random, a.turn, a.otherItem, a.rootItem, a.childItem, b.item, "msg_dd0287b00b5e5e37f0d53b128b3183c1f5daf20b115b65940a"}, malformed...)...)
	expect(owner, sessions+a.subSession+"/items", http.StatusBadRequest, itemCursor, a.childItem, a.siblingItem, a.item, b.rootItem)
	expect(owner, subagents+a.child+"/items", http.StatusBadRequest, itemCursor,
		append([]string{random, a.rootItem, a.siblingItem, a.childTurn, a.item, b.childItem}, malformed...)...)
	expect(owner, subagents+a.child+"/turns/"+a.childTurn+"/items", http.StatusBadRequest, itemCursor,
		append([]string{random, a.rootItem, a.laterChildItem, a.siblingItem, a.childTurn, b.childItem}, malformed...)...)

	// C3: Subagent and Subagent Turn lists use the resource message.
	expect(owner, sessions+a.subSession+"/subagents", http.StatusBadRequest, otherCursor,
		append([]string{random, a.rootTurn, a.childTurn, a.otherSubagent, a.session, b.child}, malformed...)...)
	expect(owner, subagents+a.child+"/turns", http.StatusBadRequest, otherCursor,
		append([]string{random, a.rootTurn, a.siblingTurn, a.otherChildTurn, a.childItem, a.child, b.childTurn}, malformed...)...)

	// C4: Artifact lists use the artifact message.
	expect(owner, sessions+a.artifactSession+"/artifacts", http.StatusBadRequest, artifact,
		append([]string{random, a.artifactTurn, a.otherArtifact, a.artifactSession, b.artifact, "artifact_e23ad32448f4ce24f27cd05ec953f6920cd9a58115308addee"}, malformed...)...)

	// C5: Skill versions tell a non-version value, another Skill's version and a
	// missing version apart; foreign and deleted versions are missing.
	versions := "/v1/skills/" + a.skill + "/versions"
	for _, value := range []string{"not-a-valid-id", a.skill, random, "3", "SKILLVER_" + random} {
		expect(owner, versions, http.StatusBadRequest, prefix(value), value)
	}
	// Long, unprintable and invalid UTF-8 values are not repeated in the message.
	unechoed := wireError("invalid_request_error", text("invalid_value"), text("after"), "Invalid 'after'. Expected an ID that begins with 'skillver'.")
	expect(owner, versions, http.StatusBadRequest, unechoed, strings.Repeat("x", 257), strings.Repeat("a", 200<<10), "bad\x01value", strings.Repeat("\x01", 1000), "line\nbreak", "\xff")
	expect(owner, versions, http.StatusBadRequest, prefix(strings.Repeat("y", 256)), strings.Repeat("y", 256))
	expect(owner, versions, http.StatusBadRequest, otherSkill, a.otherSkillVersion)
	expect(owner, versions, http.StatusNotFound, skillsMissing, "skillver_"+random, a.deletedVersion, b.version, "skillver_not-a-uuid", "skillver", "skillver_"+uuid.Nil.String())

	// K1: Files and Skills keep their existing cursor errors.
	filesMissing := wireError("invalid_request_error", nil, text("after"), "Resource not found.")
	expect(owner, "/v1/files", http.StatusNotFound, filesMissing, "file-"+random, "not-a-valid-id", b.file, a.skill)
	expect(owner, "/v1/skills", http.StatusNotFound, skillsMissing, "skill_"+random, b.skill, a.version)

	// K2: a valid cursor still returns exactly the next resource in either order,
	// with has_more and the first and last IDs of that page.
	type envelope struct {
		FirstID *string               `json:"first_id"`
		LastID  *string               `json:"last_id"`
		HasMore *bool                 `json:"has_more"`
		Data    []struct{ ID string } `json:"data"`
	}
	read := func(path string, query url.Values) (envelope, []string) {
		t.Helper()
		// The Session list is filtered to the two HTTP-created Sessions; the
		// store-seeded fixture Sessions lack a public Agent projection.
		if path == "/v1/agents/sessions" {
			query.Set("agent_id", a.agent)
		}
		status, raw := client.do(owner, http.MethodGet, path+"?"+query.Encode(), "", nil)
		var page envelope
		if status != http.StatusOK || json.Unmarshal([]byte(raw), &page) != nil || page.HasMore == nil {
			t.Fatalf("GET %s?%s: %d %s", path, query.Encode(), status, raw)
		}
		ids := make([]string, 0, len(page.Data))
		for _, value := range page.Data {
			ids = append(ids, value.ID)
		}
		return page, ids
	}
	pages := 0
	for _, path := range []string{
		"/v1/agents", "/v1/agents/sessions", sessions + a.session + "/turns", sessions + a.session + "/items",
		"/v1/agents/environments/templates", "/v1/vaults", "/v1/vaults/" + a.vault + "/credentials",
		sessions + a.subSession + "/subagents", subagents + a.child + "/items", subagents + a.child + "/turns",
		subagents + a.child + "/turns/" + a.childTurn + "/items", sessions + a.artifactSession + "/artifacts", versions,
	} {
		for _, order := range []string{"asc", "desc"} {
			_, all := read(path, url.Values{"order": {order}, "limit": {"100"}})
			if len(all) < 2 {
				t.Fatalf("K2 fixture %s has %d resources", path, len(all))
			}
			for index, cursor := range all {
				page, got := read(path, url.Values{"order": {order}, "limit": {"1"}, "after": {cursor}})
				want := all[index+1 : min(index+2, len(all))]
				bounds := page.FirstID == nil && page.LastID == nil
				if len(want) == 1 {
					bounds = page.FirstID != nil && page.LastID != nil && *page.FirstID == want[0] && *page.LastID == want[0]
				}
				if !slices.Equal(got, want) || *page.HasMore != (index+2 < len(all)) || !bounds {
					t.Errorf("%s order=%s after %d/%d: got %v has_more=%t; want %v", path, order, index, len(all), got, *page.HasMore, want)
				}
				pages++
			}
		}
	}
	if pages < 52 {
		t.Fatalf("K2 checked only %d pages", pages)
	}

	// K3: a missing or foreign parent is 404 before any cursor is evaluated.
	missingParent := func(token, path string) string {
		t.Helper()
		status, body := list(token, path, "")
		if status != http.StatusNotFound {
			t.Fatalf("missing parent %s: %d %s", path, status, body)
		}
		return body
	}
	for _, parent := range []struct{ foreign, missing string }{
		{sessions + a.session + "/turns", sessions + random + "/turns"},
		{sessions + a.session + "/items", sessions + random + "/items"},
		{sessions + a.subSession + "/subagents", sessions + random + "/subagents"},
		{subagents + a.child + "/items", sessions + random + "/subagents/" + a.child + "/items"},
		{subagents + a.child + "/turns", sessions + random + "/subagents/" + a.child + "/turns"},
		{subagents + a.child + "/turns/" + a.childTurn + "/items", sessions + random + "/subagents/" + a.child + "/turns/" + a.childTurn + "/items"},
		{sessions + a.artifactSession + "/artifacts", sessions + random + "/artifacts"},
		{"/v1/vaults/" + a.vault + "/credentials", "/v1/vaults/" + random + "/credentials"},
		{versions, "/v1/skills/skill_" + random + "/versions"},
	} {
		want := missingParent(foreign, parent.missing)
		expect(foreign, parent.foreign, http.StatusNotFound, want, "", "not-a-valid-id", random, a.item, a.childItem, a.version, b.item, b.version)
		expect(owner, parent.missing, http.StatusNotFound, want, "not-a-valid-id", a.item, a.version)
	}
	// A missing Subagent or child Turn inside an owned Session is also 404 first.
	for _, path := range []string{subagents + random + "/items", subagents + random + "/turns", subagents + a.child + "/turns/" + random + "/items", subagents + a.sibling + "/turns/" + a.childTurn + "/items"} {
		want := missingParent(owner, path)
		expect(owner, path, http.StatusNotFound, want, "not-a-valid-id", random, a.childItem, a.rootItem)
	}
	expect(owner, "/v1/skills/not-a-skill/versions", http.StatusNotFound, skillsMissing, "not-a-valid-id", a.version, a.otherSkillVersion)
}
