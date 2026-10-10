package sessionpg

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/filepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/skillpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var creator = identity.Subject{Kind: "service_account", ID: "test-runner"}

// environmentConfiguration configures a self_hosted Environment Session,
// which needs no sandbox deployment.
var environmentConfiguration = json.RawMessage(`{"agent":{"model":"m"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)

// creationService returns the Session store and service over pool with the
// built-in placement rules.
func creationService(t *testing.T, pool *pgxpool.Pool) (*Store, *sessions.Service) {
	t.Helper()
	rules, err := placement.NewRules(providers.Builtin(), "https://core.example")
	if err != nil {
		t.Fatal(err)
	}
	store := New(pgunit.NewPool(pool), pgtest.CredentialKey(t))
	service, err := sessions.NewService(store, rules)
	if err != nil {
		t.Fatal(err)
	}
	return store, service
}

func skillService(t *testing.T, pool *pgxpool.Pool) *skills.Service {
	t.Helper()
	store := skillpg.New(pgunit.NewPool(pool), pgtest.CredentialKey(t))
	service, err := skills.NewService(store, store)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func skillArchive(t *testing.T, name, marker string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.CreateHeader(&zip.FileHeader{Name: name + "/SKILL.md", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(file, "---\nname: %s\ndescription: A proof.\n---\n%s\n", name, marker); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func skillKey(t *testing.T, id string) uuid.UUID {
	t.Helper()
	key, err := skills.ParseID(id)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func sameJSON(t *testing.T, a, b any) bool {
	t.Helper()
	left, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(left, right)
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// awaitWaiters waits until count backends queue, directly or not, behind
// holder's lock.
func awaitWaiters(t *testing.T, pool *pgxpool.Pool, holder int32, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for countRows(t, pool, `WITH RECURSIVE queued(pid) AS (
 SELECT $1::int UNION SELECT a.pid FROM pg_stat_activity a JOIN queued q ON q.pid = ANY(pg_blocking_pids(a.pid))
) SELECT count(*) - 1 FROM queued`, holder) < count {
		if time.Now().After(deadline) {
			t.Fatal("lock waiters", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Concurrent creations of one intent under one key from two pools, each
// resolved to its own configuration, create one Session with the winner's
// configuration and a cursor before its initial input. A retry after the Turn
// completed and a later Turn ran returns the current Session without
// submitting the input again.
func TestCreationRetriesNeverReplayInitialInput(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := creationService(t, pool)
	_, other := creationService(t, pgtest.Open(t))
	ctx := t.Context()
	tenant := uuid.NewString()
	input := sessions.CreateSession{Creator: creator, Engine: "codex", IdempotencyKey: "initial", CreationRequest: json.RawMessage(`{"agent_id":"source"}`), InitialInputs: []sessions.Input{messageInput("first"), messageInput("second")}}
	const count = 8
	var wg sync.WaitGroup
	results := make(chan sessions.Creation, count)
	for i := range count {
		wg.Go(func() {
			creating := service
			if i%2 == 0 {
				creating = other
			}
			resolved := input
			resolved.Configuration = json.RawMessage(fmt.Sprintf(`{"resolved":%d}`, i))
			result, err := creating.CreateSession(ctx, tenant, resolved)
			if err != nil {
				t.Error(err)
				return
			}
			results <- result
		})
	}
	wg.Wait()
	close(results)
	var created []sessions.Creation
	id, configuration := "", json.RawMessage(nil)
	for result := range results {
		if id == "" {
			id, configuration = result.Session.ID, result.Session.Configuration
		}
		if result.Session.ID != id || !bytes.Equal(result.Session.Configuration, configuration) || result.Session.LastTurn == nil {
			t.Fatal("creation retry diverged", result)
		}
		if result.Created {
			created = append(created, result)
		}
	}
	if len(created) != 1 || created[0].Cursor != 0 || created[0].Session.LastTurn.Status != sessions.TurnQueued {
		t.Fatal("one creation starts before its inputs", created)
	}
	events, err := store.ListSessionEvents(ctx, tenant, id, created[0].Cursor)
	if err != nil || !reflect.DeepEqual(kinds(events), []string{"agent.session.turn.created", "agent.session.turn.item.added", "agent.session.in_progress", "agent.session.turn.item.added"}) {
		t.Fatal("initial input events", kinds(events), err)
	}
	inputs, err := store.ListTurnInputs(ctx, tenant, id, created[0].Session.LastTurn.ID, 0, 100)
	if err != nil || len(inputs) != 2 || !strings.Contains(string(inputs[0].Payload), "first") || !strings.Contains(string(inputs[1].Payload), "second") {
		t.Fatal("initial inputs", inputs, err)
	}
	// The creation returns the projection a read and a stream snapshot see.
	read, err := store.GetSession(ctx, tenant, id)
	if err != nil || !sameJSON(t, created[0].Session, read) {
		t.Fatal("creation projection differs from the Session read", err)
	}
	if snapshot, cursor, err := store.SessionStreamSnapshot(ctx, tenant, id); err != nil || !sameJSON(t, snapshot, read) || cursor != events[len(events)-1].Sequence {
		t.Fatal("stream snapshot", cursor, err)
	}
	if _, _, err := store.SessionStreamSnapshot(ctx, uuid.NewString(), id); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign stream snapshot", err)
	}
	tenantID, sessionID := pgID(uuid.MustParse(tenant)), pgID(uuid.MustParse(id))
	first := created[0].Session.LastTurn.ID
	move(t, pool, tenantID, sessionID, first, sessions.TurnQueued, sessions.TurnInProgress)
	move(t, pool, tenantID, sessionID, first, sessions.TurnInProgress, sessions.TurnCompleted)
	// The ended Turn's idle snapshot is recorded as settled.
	if ended, err := store.ListSessionEvents(ctx, tenant, id, events[len(events)-1].Sequence); err != nil || len(ended) == 0 || ended[len(ended)-1].Event.Type != "agent.session.idle" || !ended[len(ended)-1].Settled {
		t.Fatal("terminal idle is not settled", kinds(ended), err)
	}
	// The creation key at the events endpoint is an independent request.
	later := submitMessage(t, service, tenantID, sessionID, input.IdempotencyKey)
	all, err := store.ListSessionEvents(ctx, tenant, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.CreateSession(ctx, tenant, input)
	if err != nil || retry.Created || retry.Session.ID != id || !bytes.Equal(retry.Session.Configuration, configuration) || retry.Session.LastTurn.ID != later.TurnID || retry.Cursor != all[len(all)-1].Sequence {
		t.Fatal("retry after later Turn", retry, err)
	}
	if after, err := store.ListSessionEvents(ctx, tenant, id, retry.Cursor); err != nil || len(after) != 0 {
		t.Fatal("retry replayed initial input", kinds(after), err)
	}
	if turns := countRows(t, pool, "SELECT count(*) FROM turns WHERE session_id=$1", id); turns != 2 {
		t.Fatal("retry created a Turn", turns)
	}
	foreign, err := service.CreateSession(ctx, uuid.NewString(), input)
	if err != nil || !foreign.Created || foreign.Session.ID == id {
		t.Fatal("tenant creation keys collided", foreign, err)
	}
}

// A stored intent hash identifies the creation whatever the request resolved
// to, and a changed intent conflicts at both lookup and upsert; a Session
// without one falls back to its request hash and is never given one. Another
// creator, a Session without a complete creator and a deleted Session conflict
// at both lookup and upsert, and of two creators racing for one key exactly
// one creates.
func TestCreationIdentity(t *testing.T) {
	pool := pgtest.Open(t)
	_, service := creationService(t, pool)
	ctx := t.Context()
	tenant := uuid.NewString()
	request := json.RawMessage(`{"agent_id":"source","agent":{"tools":[{"parameters":{"const":9007199254740993}}]}}`)
	input := sessions.CreateSession{Creator: creator, Engine: "codex", IdempotencyKey: "intent", CreationRequest: request, Configuration: json.RawMessage(`{"resolved":1}`)}
	first, err := service.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	resolved := input
	resolved.Configuration = json.RawMessage(`{"resolved":2}`)
	if retry, err := service.CreateSession(ctx, tenant, resolved); err != nil || retry.Created || retry.Session.ID != first.Session.ID || !bytes.Equal(retry.Session.Configuration, first.Session.Configuration) {
		t.Fatal("intent retry", retry, err)
	}
	reordered := json.RawMessage(`{"agent":{"tools":[{"parameters":{"const":9007199254740993}}]},"agent_id":"source"}`)
	if found, err := service.FindSessionCreation(ctx, tenant, "intent", reordered, creator); err != nil || found.Created || found.Session.ID != first.Session.ID {
		t.Fatal("intent lookup", found, err)
	}
	if _, err := service.FindSessionCreation(ctx, tenant, "intent", json.RawMessage(`{"agent_id":"source","agent":{"tools":[{"parameters":{"const":9007199254740992}}]}}`), creator); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("changed intent found", err)
	}
	changed := input
	changed.CreationRequest = json.RawMessage(`{"agent_id":"changed"}`)
	if _, err := service.CreateSession(ctx, tenant, changed); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("changed intent upserted", err)
	}
	if _, err := service.FindSessionCreation(ctx, uuid.NewString(), "intent", request, creator); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign lookup", err)
	}

	historical := sessions.CreateSession{Creator: creator, Engine: "codex", IdempotencyKey: "historical", Configuration: json.RawMessage(`{"agent":{"instructions":"original"}}`)}
	stored, err := service.CreateSession(ctx, tenant, historical)
	if err != nil {
		t.Fatal(err)
	}
	historical.CreationRequest = json.RawMessage(`{"agent_id":"source"}`)
	if _, err := service.FindSessionCreation(ctx, tenant, "historical", historical.CreationRequest, creator); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("lookup without a stored intent", err)
	}
	if retry, err := service.CreateSession(ctx, tenant, historical); err != nil || retry.Session.ID != stored.Session.ID {
		t.Fatal("request hash retry", retry, err)
	}
	if countRows(t, pool, "SELECT count(*) FROM sessions WHERE id=$1 AND creation_request_hash IS NULL", stored.Session.ID) != 1 {
		t.Fatal("retry stored an intent")
	}
	historical.Configuration = json.RawMessage(`{"agent":{"instructions":"changed"}}`)
	if _, err := service.CreateSession(ctx, tenant, historical); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("changed request accepted", err)
	}

	another := identity.Subject{Kind: "user", ID: creator.ID}
	conflicts := func(name string, subject identity.Subject) {
		t.Helper()
		if _, err := service.FindSessionCreation(ctx, tenant, "intent", request, subject); !errors.Is(err, sessions.ErrIdempotencyConflict) {
			t.Fatal(name, "lookup", err)
		}
		retry := input
		retry.Creator = subject
		if _, err := service.CreateSession(ctx, tenant, retry); !errors.Is(err, sessions.ErrIdempotencyConflict) {
			t.Fatal(name, "upsert", err)
		}
	}
	conflicts("another creator", another)
	exec(t, pool, "UPDATE sessions SET creator_kind=NULL, creator_id=NULL WHERE id=$1", first.Session.ID)
	conflicts("unknown creator", creator)
	exec(t, pool, "UPDATE sessions SET creator_kind=$2, creator_id=$3, deleted_at=clock_timestamp() WHERE id=$1", first.Session.ID, creator.Kind, creator.ID)
	conflicts("deleted Session", creator)

	outcomes := make(chan error, 2)
	for _, subject := range []identity.Subject{creator, another} {
		go func() {
			raced := input
			raced.IdempotencyKey, raced.Creator = "raced", subject
			_, err := service.CreateSession(ctx, tenant, raced)
			outcomes <- err
		}()
	}
	won, conflicted := 0, 0
	for range 2 {
		switch err := <-outcomes; {
		case err == nil:
			won++
		case errors.Is(err, sessions.ErrIdempotencyConflict):
			conflicted++
		default:
			t.Fatal("racing creator", err)
		}
	}
	if won != 1 || conflicted != 1 {
		t.Fatal("racing creators", won, conflicted)
	}
}

// The provider-key fingerprint in retry identities depends on the credential
// key.
func TestProviderKeyFingerprintIsKeyed(t *testing.T) {
	fingerprint := func(seed byte) string {
		cipher, err := credentialcrypto.New(bytes.Repeat([]byte{seed}, 32))
		if err != nil {
			t.Fatal(err)
		}
		value, err := New(nil, cipher).FingerprintProviderKey("provider-key")
		if err != nil || value == "" || strings.Contains(value, "provider-key") {
			t.Fatal(value, err)
		}
		return value
	}
	if fingerprint(71) == fingerprint(72) {
		t.Fatal("fingerprint ignores the credential key")
	}
}

// A new Session freezes its provider, Skills and initial files sealed under
// its own binding. A retry returns it after every source is gone, changed
// bytes conflict, and a foreign source is missing.
func TestCreationFreezesResourcesOnce(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := creationService(t, pool)
	skillsService := skillService(t, pool)
	filesService, err := files.NewService(filepg.New(pgunit.NewPool(pool)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	const canary = "private-creation-canary"
	skill, err := skillsService.CreateSkill(ctx, skills.CreateSkill{TenantID: tenant, Archive: skillArchive(t, "referenced", canary+"-1")})
	if err != nil {
		t.Fatal(err)
	}
	latest := skillArchive(t, "referenced", canary+"-2")
	if _, err := skillsService.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: skillKey(t, skill.ID), Archive: latest}); err != nil {
		t.Fatal(err)
	}
	source, err := filesService.Create(ctx, files.CreateCommand{TenantID: tenant, Upload: func(w io.Writer) (files.Upload, error) {
		_, err := io.WriteString(w, canary)
		return files.Upload{Filename: "source.bin", Purpose: files.PurposeUserData}, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	inline := environmentconfig.Skill{Metadata: environmentconfig.SkillMetadata{Type: "inline", Name: "inline", Description: "A proof."}, Archive: skillArchive(t, "inline", canary)}
	reference := environmentconfig.Skill{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: skill.ID, Version: "latest"}}
	fileID := environmentconfig.InitialFile{Type: "file_id", Path: "/workspace/b", FileID: source.ID}
	input := sessions.CreateSession{
		Creator: creator, Engine: "codex", IdempotencyKey: "frozen", Configuration: environmentConfiguration,
		ModelProvider:       &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.com/v1", APIKey: canary},
		ModelProviderSource: v1.ExecutionSourceSession,
		Initialization:      environmentconfig.Setup{Skills: []environmentconfig.Skill{inline, reference}},
		InitialFiles:        []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/a", Data: []byte(canary)}, fileID},
	}
	created, err := service.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	id := created.Session.ID
	if bytes.Contains(created.Session.Configuration, []byte(canary)) || !bytes.Contains(created.Session.Configuration, []byte(`"model_provider_configured":true`)) {
		t.Fatal("Session configuration", string(created.Session.Configuration))
	}
	sealed := `FROM (SELECT encrypted_config AS b FROM session_model_execution WHERE session_id=$1
 UNION ALL SELECT contents FROM environment_setups WHERE session_id=$1
 UNION ALL SELECT contents FROM initial_environment_files WHERE session_id=$1) frozen`
	if countRows(t, pool, "SELECT count(*) "+sealed, id) != 4 || countRows(t, pool, "SELECT count(*) "+sealed+" WHERE position($2::bytea IN b) > 0", id, []byte(canary)) != 0 {
		t.Fatal("frozen data is missing or not sealed")
	}
	assertFrozen := func() {
		t.Helper()
		provider, err := store.SessionModelExecution(ctx, tenant, id)
		if err != nil || provider.APIKey != canary {
			t.Fatal("frozen provider", err)
		}
		setup, err := store.ReadEnvironmentSetup(ctx, tenant, id)
		if err != nil || len(setup.Skills) != 2 || !reflect.DeepEqual(setup.Skills[0], inline) || setup.Skills[1].Metadata.Version != "2" || !bytes.Equal(setup.Skills[1].Archive, latest) {
			t.Fatal("frozen Skills", err)
		}
		for position := range input.InitialFiles {
			if _, body, err := store.ReadInitialEnvironmentFile(ctx, tenant, id, position); err != nil || string(body) != canary {
				t.Fatal("frozen initial file", position, err)
			}
		}
	}
	assertFrozen()
	for _, reference := range []sessions.CreateSession{
		{Creator: creator, Engine: "codex", IdempotencyKey: "foreign-skill", Configuration: environmentConfiguration, Initialization: environmentconfig.Setup{Skills: []environmentconfig.Skill{reference}}},
		{Creator: creator, Engine: "codex", IdempotencyKey: "foreign-file", Configuration: environmentConfiguration, InitialFiles: []environmentconfig.InitialFile{fileID}},
	} {
		if _, err := service.CreateSession(ctx, foreign, reference); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatal(reference.IdempotencyKey, err)
		}
	}
	if countRows(t, pool, "SELECT count(*) FROM sessions WHERE tenant_id=$1", foreign) != 0 {
		t.Fatal("a failed creation left a Session")
	}
	if err := filesService.Delete(ctx, files.DeleteCommand{TenantID: tenant, FileID: source.ID}); err != nil {
		t.Fatal(err)
	}
	if err := skillsService.DeleteSkill(ctx, skills.DeleteSkill{TenantID: tenant, SkillID: skillKey(t, skill.ID)}); err != nil {
		t.Fatal(err)
	}
	if retry, err := service.CreateSession(ctx, tenant, input); err != nil || retry.Created || retry.Session.ID != id {
		t.Fatal("retry resolved its sources again", retry, err)
	}
	assertFrozen()
	changed := input
	changed.InitialFiles = []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/a", Data: []byte("changed")}, fileID}
	if _, err := service.CreateSession(ctx, tenant, changed); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("changed bytes accepted", err)
	}
}

// An Environment Session reserves its initial input, tracking its activity,
// instead of admitting a Turn.
func TestEnvironmentCreationReservesItsInitialInput(t *testing.T) {
	pool := pgtest.Open(t)
	_, service := creationService(t, pool)
	input := sessions.CreateSession{Creator: creator, Engine: "codex", IdempotencyKey: "environment", InitialInputs: []sessions.Input{messageInput("first")},
		Configuration: json.RawMessage(`{"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)}
	created, err := service.CreateSession(t.Context(), uuid.NewString(), input)
	if err != nil || created.Session.Environment == nil || created.Session.LastTurn != nil || created.Session.EnvironmentInputActivity == nil {
		t.Fatal(created, err)
	}
	if turns := countRows(t, pool, "SELECT count(*) FROM turns WHERE session_id=$1", created.Session.ID); turns != 0 {
		t.Fatal("initial input admitted a Turn", turns)
	}
	if reserved := countRows(t, pool, "SELECT count(*) FROM environment_input_reservations WHERE session_id=$1 AND is_initial", created.Session.ID); reserved != 1 {
		t.Fatal("initial reservations", reserved)
	}
}

// Hosted creation checks admission on the locked deployment, so it sees a
// reset committed while it waited, and places after creating the
// Environment, rolling both back when no node is available. A retry admits
// nothing and still returns its Session.
func TestHostedCreationAdmitsAndPlacesUnderTheDeploymentLock(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	_, service := creationService(t, pool)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tenant := uuid.NewString()
	nodes, err := json.Marshal(sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}, Runtime: &sandbox.RuntimeRelease{SourceCommit: strings.Repeat("a", 40),
		ImageID: "sha256:" + strings.Repeat("b", 64), ImageManifestDigest: "sha256:" + strings.Repeat("c", 64), MicrosandboxRef: "oac-runtime@sha256:" + strings.Repeat("d", 64),
		RuntimeSHA256: strings.Repeat("e", 64), FirmwareSHA256: strings.Repeat("f", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	// A direct deployment admits hosted work without placing it on a node.
	exec(t, pool, `UPDATE runtime_deployment SET installation_id=$1, backend_fingerprint=$2, provider_kind='e2b', mode='direct', generation=1,
		specification='{"resources":{"cpus":2,"memory_mib":2048}}'`, uuid.New(), strings.Repeat("a", 64))
	hosted := func(key string) sessions.CreateSession {
		return sessions.CreateSession{Creator: creator, Engine: "codex", IdempotencyKey: key, Configuration: json.RawMessage(`{"agent":{"model":"m"},"environment":{"type":"openai_hosted"}}`)}
	}
	existing, err := service.CreateSession(ctx, tenant, hosted("existing"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var holder int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid() FROM runtime_deployment FOR UPDATE").Scan(&holder); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := service.CreateSession(ctx, tenant, hosted("paused"))
		done <- err
	}()
	awaitBlocked(ctx, t, pool, holder)
	if _, err := tx.Exec(ctx, `UPDATE runtime_deployment SET provider_kind='docker', mode='nodes', generation=2, specification=$1,
		reset_clear='force', reset_requested_at=now(), reset_forced_at=now(), reset_audit='{}'`, string(nodes)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, placement.ErrResetAdmission) {
		t.Fatal("creation bypassed the committed reset", err)
	}
	if retry, err := service.CreateSession(ctx, tenant, hosted("existing")); err != nil || retry.Created || retry.Session.ID != existing.Session.ID {
		t.Fatal("retry ran admission", retry, err)
	}
	exec(t, pool, "UPDATE runtime_deployment SET reset_clear=NULL, reset_requested_at=NULL, reset_forced_at=NULL, reset_audit=NULL")
	if _, err := service.CreateSession(ctx, tenant, hosted("unplaced")); !errors.Is(err, placement.ErrNodeUnavailable) {
		t.Fatal("placement without a node", err)
	}
	if count := countRows(t, pool, "SELECT count(*) FROM sessions s JOIN environments e ON e.session_id=s.id WHERE s.tenant_id=$1", tenant); count != 1 {
		t.Fatal("failed hosted creation left work", count)
	}
}

// Freezing a Skill version and deleting it serialize on the Skill lock:
// whichever goes first decides, and no Session refers to a missing version.
func TestSkillFreezeSerializesWithVersionDeletion(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := creationService(t, pool)
	skillsService := skillService(t, pool)
	ctx := t.Context()
	tenant := uuid.NewString()
	for _, freezeFirst := range []bool{true, false} {
		skill, err := skillsService.CreateSkill(ctx, skills.CreateSkill{TenantID: tenant, Archive: skillArchive(t, "raced", "one")})
		if err != nil {
			t.Fatal(err)
		}
		key := skillKey(t, skill.ID)
		second := skillArchive(t, "raced", "two")
		if _, err := skillsService.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: key, Archive: second}); err != nil {
			t.Fatal(err)
		}
		holder, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var holderPID int32
		if err := holder.QueryRow(ctx, "SELECT pg_backend_pid() FROM skills WHERE id=$1 FOR UPDATE", key).Scan(&holderPID); err != nil {
			t.Fatal(err)
		}
		input := sessions.CreateSession{Creator: creator, Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: environmentConfiguration,
			Initialization: environmentconfig.Setup{Skills: []environmentconfig.Skill{{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: skill.ID, Version: "2"}}}}}
		type outcome struct {
			creation sessions.Creation
			err      error
		}
		created, deleted := make(chan outcome, 1), make(chan error, 1)
		freeze := func() {
			creation, err := service.CreateSession(ctx, tenant, input)
			created <- outcome{creation, err}
		}
		remove := func() {
			_, err := skillsService.DeleteVersion(ctx, skills.DeleteVersion{TenantID: tenant, SkillID: key, Version: 2})
			deleted <- err
		}
		early, late := freeze, remove
		if !freezeFirst {
			early, late = remove, freeze
		}
		go early()
		awaitWaiters(t, pool, holderPID, 1)
		go late()
		awaitWaiters(t, pool, holderPID, 2)
		if err := holder.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		creation, err := <-created, <-deleted
		if err != nil {
			t.Fatal("version deletion", err)
		}
		if !freezeFirst {
			if !errors.Is(creation.err, sessions.ErrNotFound) || countRows(t, pool, "SELECT count(*) FROM sessions WHERE tenant_id=$1 AND idempotency_key=$2", tenant, input.IdempotencyKey) != 0 {
				t.Fatal("creation froze a deleted version", creation.err)
			}
			continue
		}
		if creation.err != nil {
			t.Fatal(creation.err)
		}
		setup, err := store.ReadEnvironmentSetup(ctx, tenant, creation.creation.Session.ID)
		if err != nil || setup.Skills[0].Metadata.Version != "2" || !bytes.Equal(setup.Skills[0].Archive, second) {
			t.Fatal("frozen version after its deletion", err)
		}
	}
}

// Every creation, retries included, records a create audit; only the new
// Session lists what it created. An audit that cannot be recorded fails the
// creation closed and rolls it back.
func TestCreationAudit(t *testing.T) {
	pool := pgtest.Open(t)
	_, service := creationService(t, pool)
	tenant := uuid.NewString()
	source := writeaudit.Source{KeyID: uuid.NewString(), Prefix: "pc_aaaaaaaa", Name: "test key", Kind: "issued", TenantID: uuid.NewString(), RequestID: uuid.NewString(), TraceID: uuid.NewString()}
	input := sessions.CreateSession{Creator: creator, Engine: "codex", IdempotencyKey: "audited", InitialInputs: []sessions.Input{messageInput("first")},
		Configuration: json.RawMessage(`{"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)}
	if _, err := service.CreateSession(writeaudit.WithSource(t.Context(), source), tenant, input); !errors.Is(err, writeaudit.ErrInvalidSource) {
		t.Fatal("foreign provenance recorded", err)
	}
	if countRows(t, pool, "SELECT count(*) FROM sessions WHERE tenant_id=$1", tenant) != 0 {
		t.Fatal("a failed audit left the Session")
	}
	source.TenantID = tenant
	ctx := writeaudit.WithSource(t.Context(), source)
	created, err := service.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	// A retry is another request.
	source.RequestID = uuid.NewString()
	if _, err := service.CreateSession(writeaudit.WithSource(t.Context(), source), tenant, input); err != nil {
		t.Fatal(err)
	}
	if count := countRows(t, pool, "SELECT count(*) FROM write_audit_operations WHERE tenant_id=$1 AND action='create' AND resource_type='session' AND resource_id=$2", tenant, created.Session.ID); count != 2 {
		t.Fatal("create audits", count)
	}
	var owned []string
	rows, err := pool.Query(t.Context(), "SELECT resource_type FROM write_audit_owners WHERE tenant_id=$1 ORDER BY resource_type", tenant)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var resource string
		if err := rows.Scan(&resource); err != nil {
			t.Fatal(err)
		}
		owned = append(owned, resource)
	}
	if rows.Err() != nil || !reflect.DeepEqual(owned, []string{"environment", "session"}) {
		t.Fatal("created resources", owned, rows.Err())
	}
}

// Profile admission uses the deployment selected while holding its lock, so a
// storage-mode update cannot race a preflight check and persist unsupported work.
func TestHostedExternalHistoryAdmissionRollsBackAfterDeploymentChange(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	store, _ := creationService(t, pool)
	rules, err := placement.NewRules(externalAdmissionDeclarations{}, "https://core.example")
	if err != nil {
		t.Fatal(err)
	}
	service, err := sessions.NewService(store, rules)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tenant := uuid.NewString()
	exec(t, pool, `UPDATE runtime_deployment SET installation_id=$1, backend_fingerprint=$2, provider_kind='test-direct', mode='direct', generation=1, specification='{"resources":{"cpus":2,"memory_mib":2048}}'`, uuid.New(), strings.Repeat("a", 64))
	input := sessions.CreateSession{Creator: creator, Engine: "codex", IdempotencyKey: "raced", Configuration: json.RawMessage(`{"agent":{"model":"m"},"environment":{"type":"openai_hosted"}}`)}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var holder int32
	if err = tx.QueryRow(ctx, "SELECT pg_backend_pid() FROM runtime_deployment FOR UPDATE").Scan(&holder); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := service.CreateSession(ctx, tenant, input); done <- err }()
	awaitBlocked(ctx, t, pool, holder)
	if _, err = tx.Exec(ctx, `UPDATE runtime_deployment SET specification=jsonb_set(specification,'{workspace}','{}')`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal("external combination admitted", err)
	}
	if count := countRows(t, pool, "SELECT count(*) FROM sessions WHERE tenant_id=$1", tenant); count != 0 {
		t.Fatal("rejected admission persisted Session", count)
	}
	input.SupportsRetainedNativeHistory = true
	created, err := service.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	input.SupportsRetainedNativeHistory = false
	retry, err := service.CreateSession(ctx, tenant, input)
	if err != nil || retry.Created || retry.Session.ID != created.Session.ID {
		t.Fatal("trusted admission input changed retry identity", retry, err)
	}
}

// This fixture declares a direct provider supporting both storage modes.
type externalAdmissionDeclarations struct{}

func (externalAdmissionDeclarations) RequiresPublicOrigin(string) (bool, error) { return false, nil }
func (externalAdmissionDeclarations) ValidateSpecification(string, sandbox.DeploymentSpec) error {
	return nil
}
