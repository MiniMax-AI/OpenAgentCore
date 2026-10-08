package sessions

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// fakeCreationTx is fakeInputTx with the creation methods, equally strict.
type fakeCreationTx struct {
	*fakeInputTx

	upsertSession              func(NewSession) (Creation, error)
	lockDeployment             func() (placement.Deployment, error)
	loadNodes                  func() ([]placement.Node, error)
	reservePlacement           func() error
	lockSkills                 func() (map[string]skills.Skill, error)
	readSkillVersion           func() (skills.Content, error)
	saveModelExecution         func() error
	saveExecutionConfiguration func() error
	saveInitialFiles           func() error
	saveSetup                  func() error
	createEnvironment          func() (string, error)
	pruneChanges               func() error
	auditCreation              func() error
	loadSession                func() (Session, error)
}

var _ CreationTx = (*fakeCreationTx)(nil)

func (f *fakeCreationTx) UpsertSession(_ context.Context, session NewSession) (Creation, error) {
	f.record("UpsertSession", f.upsertSession != nil, session.Key)
	return f.upsertSession(session)
}

func (f *fakeCreationTx) LockDeployment(context.Context) (placement.Deployment, error) {
	f.record("LockDeployment", f.lockDeployment != nil)
	return f.lockDeployment()
}

func (f *fakeCreationTx) LoadNodes(context.Context) ([]placement.Node, error) {
	f.record("LoadNodes", f.loadNodes != nil)
	return f.loadNodes()
}

func (f *fakeCreationTx) ReservePlacement(_ context.Context, chosen placement.Placement) error {
	f.record("ReservePlacement", f.reservePlacement != nil, chosen.NodeID, fmt.Sprint(chosen.Generation))
	return f.reservePlacement()
}

func (f *fakeCreationTx) LockSkills(_ context.Context, ids []string) (map[string]skills.Skill, error) {
	f.record("LockSkills", f.lockSkills != nil, ids...)
	return f.lockSkills()
}

func (f *fakeCreationTx) ReadSkillVersion(_ context.Context, skill string, version int64) (skills.Content, error) {
	f.record("ReadSkillVersion", f.readSkillVersion != nil, skill, fmt.Sprint(version))
	return f.readSkillVersion()
}

func (f *fakeCreationTx) SaveModelExecution(_ context.Context, provider v1.ModelProviderInput) error {
	f.record("SaveModelExecution", f.saveModelExecution != nil, provider.BaseURL)
	return f.saveModelExecution()
}

func (f *fakeCreationTx) SaveExecutionConfiguration(_ context.Context, projection v1.SessionExecutionConfiguration, revision uuid.UUID) error {
	f.record("SaveExecutionConfiguration", f.saveExecutionConfiguration != nil, projection.ModelProvider.Status, revision.String())
	return f.saveExecutionConfiguration()
}

func (f *fakeCreationTx) SaveInitialFiles(_ context.Context, files []environmentconfig.InitialFile) error {
	f.record("SaveInitialFiles", f.saveInitialFiles != nil, fmt.Sprint(len(files)))
	return f.saveInitialFiles()
}

func (f *fakeCreationTx) SaveSetup(_ context.Context, setup environmentconfig.Setup) error {
	var frozen []string
	for _, skill := range setup.Skills {
		frozen = append(frozen, skill.Metadata.SkillID+"@"+skill.Metadata.Version)
	}
	f.record("SaveSetup", f.saveSetup != nil, frozen...)
	return f.saveSetup()
}

func (f *fakeCreationTx) CreateEnvironment(context.Context) (string, error) {
	f.record("CreateEnvironment", f.createEnvironment != nil)
	return f.createEnvironment()
}

func (f *fakeCreationTx) PruneChanges(context.Context) error {
	f.record("PruneChanges", f.pruneChanges != nil)
	return f.pruneChanges()
}

func (f *fakeCreationTx) AuditCreation(_ context.Context, created ...writeaudit.Resource) error {
	var resources []string
	for _, resource := range created {
		resources = append(resources, strings.TrimSuffix(resource.Type+":"+resource.ID+":"+resource.ParentID, ":"))
	}
	f.record("AuditCreation", f.auditCreation != nil, resources...)
	return f.auditCreation()
}

func (f *fakeCreationTx) LoadSession(context.Context) (Session, error) {
	f.record("LoadSession", f.loadSession != nil)
	return f.loadSession()
}

func (s *fakeStorage) FingerprintProviderKey(secret string) (string, error) {
	s.record("FingerprintProviderKey", s.fingerprint != nil)
	return s.fingerprint(secret)
}

func (s *fakeStorage) WithCreation(ctx context.Context, tenant string, apply func(context.Context, CreationTx) error) error {
	s.record("WithCreation", s.creation != nil, tenant)
	return apply(ctx, s.creation)
}

func (s *fakeStorage) FindCreation(_ context.Context, tenant, key string) (CreationRecord, error) {
	s.record("FindCreation", s.findCreation != nil, tenant, key)
	return s.findCreation()
}

// fingerprints is a fake FingerprintProviderKey that keeps keys apart.
func fingerprints(secret string) (string, error) { return "fp-" + secret, nil }

var errFingerprint = errors.New("fingerprint failed")

// failing is a fake FingerprintProviderKey that fails.
func failing(string) (string, error) { return "", errFingerprint }

// declarations accept every provider and specification.
type declarations struct{}

func (declarations) ValidateSpecification(string, sandbox.DeploymentSpec) error { return nil }

var (
	creator  = identity.Subject{Kind: "user", ID: "creator"}
	provider = &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.example/v1", APIKey: "secret"}
)

func creationInput(environment string) CreateSession {
	return CreateSession{
		Creator: creator, Engine: "codex", IdempotencyKey: "create", Metadata: map[string]string{"b": "2", "a": "1"},
		Configuration: json.RawMessage(`{"environment":{"type":"` + environment + `"},"agent":{"model":"gpt"}}`),
	}
}

func TestPrepareCreation(t *testing.T) {
	base := creationInput("self_hosted")
	prepare := func(t *testing.T, change func(*CreateSession), fingerprint func(string) (string, error)) NewSession {
		t.Helper()
		input := base
		change(&input)
		session, _, _, err := prepareCreation(input, fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	withProvider := func(environment, key, source string) func(*CreateSession) {
		return func(input *CreateSession) {
			copy := *provider
			copy.APIKey = key
			input.Configuration = creationInput(environment).Configuration
			input.ModelProvider, input.ModelProviderSource = &copy, source
		}
	}
	session := prepare(t, func(*CreateSession) {}, fingerprints)
	if session.Key != "create" || session.Engine != "codex" || session.Creator != creator || session.IntentHash != nil || string(session.Metadata) != `{"a":"1","b":"2"}` {
		t.Fatalf("session %+v", session)
	}
	if prepare(t, func(input *CreateSession) {
		input.Metadata = map[string]string{"a": "1", "b": "2"}
		input.Engine = " codex "
	}, fingerprints).RequestHash != session.RequestHash {
		t.Fatal("metadata order or engine whitespace changed the identity")
	}
	inputs := []Input{messageInput("first"), messageInput("second")}
	initial := prepare(t, func(input *CreateSession) { input.InitialInputs = inputs }, fingerprints).RequestHash
	for _, changed := range [][]Input{nil, {messageInput("changed")}, {inputs[1], inputs[0]}} {
		if prepare(t, func(input *CreateSession) { input.InitialInputs = changed }, fingerprints).RequestHash == initial {
			t.Fatal("changed initial input kept the identity", changed)
		}
	}
	if prepare(t, withProvider("self_hosted", "one", "session"), fingerprints).RequestHash == prepare(t, withProvider("self_hosted", "two", "session"), fingerprints).RequestHash {
		t.Fatal("caller keys share an identity")
	}
	deployed := prepare(t, withProvider("none", "deployment-key", v1.ModelProviderSourceDeployment), failing)
	plain := prepare(t, func(input *CreateSession) { input.Configuration = creationInput("none").Configuration }, failing)
	if deployed.RequestHash != plain.RequestHash {
		t.Fatalf("the deployment default joined the identity: %s", deployed.Configuration)
	}
	if intent := prepare(t, func(input *CreateSession) { input.CreationRequest = json.RawMessage(`{"agent_id":"a"}`) }, fingerprints).IntentHash; intent == nil {
		t.Fatal("no intent hash")
	}

	for name, test := range map[string]struct {
		change      func(*CreateSession)
		fingerprint func(string) (string, error)
		want        error
	}{
		"invalid creator": {func(input *CreateSession) { input.Creator = identity.Subject{} }, fingerprints, ErrInvalidInput},
		"invalid engine":  {func(input *CreateSession) { input.Engine = "Codex" }, fingerprints, ErrInvalidInput},
		"blank key":       {func(input *CreateSession) { input.IdempotencyKey = " " }, fingerprints, ErrInvalidInput},
		"long key":        {func(input *CreateSession) { input.IdempotencyKey = strings.Repeat("k", 129) }, fingerprints, ErrInvalidInput},
		"configuration over 512K": {func(input *CreateSession) {
			input.Configuration = json.RawMessage(`"` + strings.Repeat("x", 512*1024) + `"`)
		}, fingerprints, ErrInvalidInput},
		"configuration array":  {func(input *CreateSession) { input.Configuration = json.RawMessage(`[]`) }, fingerprints, ErrInvalidInput},
		"cancel initial input": {func(input *CreateSession) { input.InitialInputs = []Input{cancelInput} }, fingerprints, ErrInvalidInput},
		"fingerprint failure":  {withProvider("self_hosted", "key", "session"), failing, errFingerprint},
		"unreadable intent":    {func(input *CreateSession) { input.CreationRequest = json.RawMessage(`[]`) }, fingerprints, ErrInvalidInput},
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			test.change(&input)
			if _, _, _, err := prepareCreation(input, test.fingerprint); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestIntentHash(t *testing.T) {
	hash := func(raw string, fingerprint func(string) (string, error)) (*string, error) {
		return intentHash(json.RawMessage(raw), fingerprint)
	}
	if value, err := hash("", fingerprints); value != nil || err != nil {
		t.Fatal("empty intent", value, err)
	}
	one, err := hash(`{"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"u","api_key":"one"}},"b":1}`, fingerprints)
	if err != nil {
		t.Fatal(err)
	}
	reordered, _ := hash(` {"b":1,"x_agents_core":{"model_provider":{"api_key":"one","base_url":"u","protocol":"responses"}}}`, fingerprints)
	two, _ := hash(`{"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"u","api_key":"two"}},"b":1}`, fingerprints)
	if one == nil || *one != *reordered || *one == *two {
		t.Fatal("intent hash ignores order or confuses keys")
	}
	for name, test := range map[string]struct {
		raw         string
		fingerprint func(string) (string, error)
		want        error
	}{
		"not an object":        {`[]`, fingerprints, ErrInvalidInput},
		"over 16 MiB":          {`"` + strings.Repeat("x", 16<<20) + `"`, fingerprints, ErrInvalidInput},
		"extension not object": {`{"x_agents_core":[]}`, fingerprints, ErrInvalidInput},
		"provider unreadable":  {`{"x_agents_core":{"model_provider":[]}}`, fingerprints, ErrInvalidInput},
		"provider fingerprint": {`{"x_agents_core":{"model_provider":{"api_key":"k"}}}`, failing, errFingerprint},
		"null extension":       {`{"x_agents_core":null}`, failing, nil},
		"null provider":        {`{"x_agents_core":{"model_provider":null}}`, failing, nil},
		"intent without key":   {`{"agent_id":"a"}`, failing, nil},
	} {
		if _, err := hash(test.raw, test.fingerprint); test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
			t.Errorf("%s: got %v, want %v", name, err, test.want)
		}
	}
}

func TestFreezeProjection(t *testing.T) {
	model, harness := "gpt", "codex"
	session := Session{ID: "session", Engine: harness, Configuration: json.RawMessage(`{"agent":{"model":"gpt"}}`)}
	revision := uuid.New()
	projection := func(source, status string, view *v1.ModelProviderView) v1.SessionExecutionConfiguration {
		return v1.SessionExecutionConfiguration{
			Model: v1.ExecutionSelection{Value: &model, Source: "session"}, Harness: v1.ExecutionSelection{Value: &harness, Source: "agent"},
			ModelProvider: v1.ExecutionProviderSelection{Source: source, Status: status, Configuration: view},
		}
	}
	other := "other"
	mismatched := projection("unknown", "", nil)
	mismatched.Harness.Value = &other
	for name, test := range map[string]struct {
		projection   v1.SessionExecutionConfiguration
		provider     *v1.ModelProviderInput
		source       string
		status       string
		revision     uuid.UUID
		want         error
		hasProjected bool
	}{
		"deployment records its revision":  {projection("deployment", "", nil), provider, v1.ModelProviderSourceDeployment, "available", revision, nil, true},
		"deployment from another source":   {projection("deployment", "", nil), provider, "session", "available", uuid.Nil, nil, true},
		"deployment without a provider":    {projection("deployment", "", nil), nil, "", "", uuid.Nil, ErrInvalidInput, false},
		"caller provider matches":          {projection("session", "available", provider.SafeView()), provider, "session", "available", uuid.Nil, nil, true},
		"caller provider mismatches":       {projection("agent", "available", &v1.ModelProviderView{BaseURL: "other"}), provider, "agent", "", uuid.Nil, ErrInvalidInput, false},
		"unknown provider is unavailable":  {projection("unknown", "available", provider.SafeView()), nil, "", "unavailable", uuid.Nil, nil, true},
		"invalid provider source":          {projection("vault", "", nil), nil, "", "", uuid.Nil, ErrInvalidInput, false},
		"harness mismatches configuration": {mismatched, nil, "", "", uuid.Nil, ErrInvalidInput, false},
	} {
		t.Run(name, func(t *testing.T) {
			frozen, recorded, err := freezeProjection(session, test.projection, test.provider, test.source, revision)
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if !test.hasProjected {
				return
			}
			if frozen.ModelProvider.Status != test.status || recorded != test.revision || frozen.SessionID != "session" || frozen.Object != "agent.session.execution_configuration" {
				t.Fatalf("frozen %+v, revision %s", frozen, recorded)
			}
		})
	}
}

func TestDecideRecovery(t *testing.T) {
	hash, other := "hash", "other"
	stranger := identity.Subject{Kind: "user", ID: "stranger"}
	for name, test := range map[string]struct {
		record CreationRecord
		want   error
	}{
		"matching intent":         {CreationRecord{Creator: &creator, IntentHash: &hash}, nil},
		"deleted":                 {CreationRecord{Deleted: true, Creator: &creator, IntentHash: &hash}, ErrIdempotencyConflict},
		"no creator":              {CreationRecord{IntentHash: &hash}, ErrIdempotencyConflict},
		"another creator":         {CreationRecord{Creator: &stranger, IntentHash: &hash}, ErrIdempotencyConflict},
		"no intent":               {CreationRecord{Creator: &creator}, ErrNotFound},
		"deleted without intent":  {CreationRecord{Deleted: true, Creator: &creator}, ErrIdempotencyConflict},
		"another intent":          {CreationRecord{Creator: &creator, IntentHash: &other}, ErrIdempotencyConflict},
		"stranger without intent": {CreationRecord{Creator: &stranger}, ErrIdempotencyConflict},
	} {
		if err := decideRecovery(test.record, creator, hash); err != test.want {
			t.Errorf("%s: got %v, want %v", name, err, test.want)
		}
	}
}

// skillZip is a Skill archive of one SKILL.md.
func skillZip(t *testing.T, name, description string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create(name + "/SKILL.md")
	if err == nil {
		_, err = fmt.Fprintf(file, "---\nname: %s\ndescription: %s\n---\n", name, description)
	}
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// newCreationTx is a strict creation transaction whose upsert creates
// session, or returns it as stored when created is false.
func newCreationTx(t *testing.T, session Session, created bool) *fakeCreationTx {
	tx := &fakeCreationTx{fakeInputTx: newInputTx(t)}
	tx.upsertSession = func(NewSession) (Creation, error) {
		return Creation{Session: session, Created: created, Cursor: 4}, nil
	}
	tx.auditCreation = done
	tx.loadSession = returns(Session{ID: session.ID, Engine: "loaded"})
	return tx
}

// runCreation creates input with the service's rules on tx and returns the
// result and the transaction's calls, with the internal input key replaced.
func runCreation(t *testing.T, rules *placement.Rules, tx *fakeCreationTx, input CreateSession) (Creation, error, []string) {
	t.Helper()
	storage := &fakeStorage{t: t, creation: tx, fingerprint: fingerprints}
	service, err := NewService(storage, rules)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.CreateSession(t.Context(), "tenant", input)
	calls := strings.Join(tx.calls, "\n")
	if key := regexp.MustCompile(`(?:CreateInputReservation|CreateTurnInput \S+) (\S+)`).FindStringSubmatch(calls); key != nil {
		if _, parseErr := uuid.Parse(key[1]); parseErr != nil {
			t.Fatalf("input key %q", key[1])
		}
		calls = strings.ReplaceAll(calls, key[1], "<key>")
	}
	return result, err, strings.Split(calls, "\n")
}

func TestCreateSession(t *testing.T) {
	rules, err := placement.NewRules(declarations{}, "https://core.example")
	if err != nil {
		t.Fatal(err)
	}
	selfHosted := Session{ID: "session", Engine: "codex", Configuration: json.RawMessage(`{"agent":{"model":"gpt"},"environment":{"type":"self_hosted"}}`)}
	hostedSession := Session{ID: "session", Engine: "codex", Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`)}
	noEnvironment := Session{ID: "session", Engine: "codex", Configuration: json.RawMessage(`{"environment":{"type":"none"}}`)}

	t.Run("a new self-hosted Session freezes, then reserves its initial input", func(t *testing.T) {
		tx := newCreationTx(t, selfHosted, true)
		tx.lockSkills = returns(map[string]skills.Skill{"skill_a": {DefaultVersion: 2, LatestVersion: 3}})
		tx.readSkillVersion = returns(skills.Content{Version: skills.Version{SkillID: "skill_a", Version: 3, Name: "proof", Description: "Verify a frozen Skill."}, Archive: skillZip(t, "proof", "Verify a frozen Skill.")})
		tx.saveModelExecution, tx.saveExecutionConfiguration, tx.saveInitialFiles, tx.saveSetup = done, done, done, done
		tx.createEnvironment = returns("environment")
		tx.loadEnvironmentInput, tx.createInputReservation, tx.pruneChanges = inputs(), returns(EnvironmentInputReservation{}), done
		input := creationInput("self_hosted")
		input.ModelProvider, input.ModelProviderSource = provider, "session"
		model, harness := "gpt", "codex"
		input.ExecutionConfiguration = &v1.SessionExecutionConfiguration{Model: v1.ExecutionSelection{Value: &model, Source: "session"}, Harness: v1.ExecutionSelection{Value: &harness, Source: "session"},
			ModelProvider: v1.ExecutionProviderSelection{Source: "session", Status: "available", Configuration: provider.SafeView()}}
		input.InitialFiles = []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/a.txt", Data: []byte("a")}}
		input.Initialization = environmentconfig.Setup{Skills: []environmentconfig.Skill{{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: "skill_a", Version: "latest"}}}}
		input.InitialInputs = []Input{messageInput("hi")}
		result, err, calls := runCreation(t, rules, tx, input)
		if err != nil || !result.Created || result.Cursor != 4 || result.Session.Engine != "loaded" {
			t.Fatalf("result %+v, %v", result, err)
		}
		want := []string{"UpsertSession create", "LockSkills skill_a", "ReadSkillVersion skill_a 3", "SaveModelExecution https://model.example/v1",
			"SaveExecutionConfiguration available " + uuid.Nil.String(), "SaveInitialFiles 1", "SaveSetup skill_a@3", "CreateEnvironment",
			"LoadEnvironmentInput", `CreateInputReservation <key> [{"kind":"message","payload":` + hi + `}] initial`, "LoadEnvironmentInput",
			"PruneChanges", "AuditCreation session:session environment:environment:session", "LoadSession"}
		if strings.Join(calls, "\n") != strings.Join(want, "\n") {
			t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
		}
	})

	t.Run("a new Session without an Environment admits its initial input", func(t *testing.T) {
		tx := newCreationTx(t, noEnvironment, true)
		tx.loadActiveTurn, tx.createTurn, tx.appendChanges = activeTurn(nil), returns(turnWith(TurnQueued)), collect(new([]SessionChange))
		tx.createTurnInput, tx.loadInputSource, tx.loadUsage, tx.pruneChanges = sequences(7), unprojected, returns(json.RawMessage(`{}`)), done
		input := creationInput("none")
		input.InitialInputs = []Input{messageInput("hi")}
		_, err, calls := runCreation(t, nil, tx, input)
		want := []string{"UpsertSession create", "LoadActiveTurn", "CreateTurn", "AppendChanges agent.session.turn.created", "CreateTurnInput " + testTurn + " <key> 0 message",
			"LoadInputSource 7", "LoadUsage", "AppendChanges agent.session.in_progress", "PruneChanges", "AuditCreation session:session", "LoadSession"}
		if err != nil || strings.Join(calls, "\n") != strings.Join(want, "\n") {
			t.Fatalf("calls:\n%s\nwant:\n%s\n%v", strings.Join(calls, "\n"), strings.Join(want, "\n"), err)
		}
	})

	t.Run("a hosted Session is admitted under the deployment lock and placed after its Environment", func(t *testing.T) {
		ready := uint64(5)
		tx := newCreationTx(t, hostedSession, true)
		tx.lockDeployment = returns(placement.Deployment{InstallationID: "installation", Provider: "docker", Specification: json.RawMessage(`{}`)})
		tx.createEnvironment = returns("environment")
		tx.loadNodes = returns([]placement.Node{{ID: "node", Online: true, ServingReady: true, ReadyGeneration: &ready, MaxActive: 1, MaxRetained: 1, CoreURL: rules.PublicURL()}})
		tx.reservePlacement = done
		_, err, calls := runCreation(t, rules, tx, creationInput("openai_hosted"))
		want := []string{"UpsertSession create", "LockDeployment", "CreateEnvironment", "LoadNodes", "ReservePlacement node 5", "AuditCreation session:session environment:environment:session", "LoadSession"}
		if err != nil || strings.Join(calls, "\n") != strings.Join(want, "\n") {
			t.Fatalf("calls %q, %v", calls, err)
		}
	})

	for _, test := range []struct {
		name  string
		rules *placement.Rules
		tx    func(*fakeCreationTx)
		want  error
		calls []string
	}{
		{"a reset closes hosted admission", rules, func(tx *fakeCreationTx) {
			tx.lockDeployment = returns(placement.Deployment{InstallationID: "installation", Provider: "docker", Resetting: true})
		}, placement.ErrResetAdmission, []string{"UpsertSession create", "LockDeployment"}},
		{"no node places the Environment", rules, func(tx *fakeCreationTx) {
			tx.lockDeployment = returns(placement.Deployment{InstallationID: "installation", Provider: "docker", Specification: json.RawMessage(`{}`)})
			tx.createEnvironment, tx.loadNodes = returns("environment"), returns([]placement.Node(nil))
		}, placement.ErrNodeUnavailable, []string{"UpsertSession create", "LockDeployment", "CreateEnvironment", "LoadNodes"}},
		{"hosted creation needs rules", nil, func(*fakeCreationTx) {}, nil, []string{"UpsertSession create"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := newCreationTx(t, hostedSession, true)
			test.tx(tx)
			_, err, calls := runCreation(t, test.rules, tx, creationInput("openai_hosted"))
			if err == nil || test.want != nil && !errors.Is(err, test.want) || strings.Join(calls, "\n") != strings.Join(test.calls, "\n") {
				t.Fatalf("calls %q, %v", calls, err)
			}
		})
	}

	t.Run("a retry only audits and reads the stored Session", func(t *testing.T) {
		tx := newCreationTx(t, hostedSession, false)
		input := creationInput("openai_hosted")
		input.InitialInputs = []Input{messageInput("hi")}
		result, err, calls := runCreation(t, nil, tx, input)
		if err != nil || result.Created || result.Cursor != 4 || strings.Join(calls, "\n") != "UpsertSession create\nAuditCreation\nLoadSession" {
			t.Fatalf("result %+v, calls %q, %v", result, calls, err)
		}
	})

	t.Run("a failed audit fails the creation", func(t *testing.T) {
		tx := newCreationTx(t, noEnvironment, true)
		tx.auditCreation = func() error { return errStorage }
		if _, err, calls := runCreation(t, nil, tx, creationInput("none")); !errors.Is(err, errStorage) || strings.Join(calls, "\n") != "UpsertSession create\nAuditCreation session:session" {
			t.Fatalf("calls %q, %v", calls, err)
		}
	})

	t.Run("invalid input never reaches storage", func(t *testing.T) {
		storage := &fakeStorage{t: t}
		service, err := NewService(storage, rules)
		if err != nil {
			t.Fatal(err)
		}
		input := creationInput("none")
		input.IdempotencyKey = ""
		if _, err := service.CreateSession(t.Context(), "tenant", input); !errors.Is(err, ErrInvalidInput) || len(storage.calls) != 0 {
			t.Fatal(err, storage.calls)
		}
	})
}

func TestFindSessionCreation(t *testing.T) {
	request := json.RawMessage(`{"agent_id":"agent"}`)
	hash, err := intentHash(request, fingerprints)
	if err != nil {
		t.Fatal(err)
	}
	found := func() (CreationRecord, error) {
		return CreationRecord{SessionID: "session", Creator: &creator, IntentHash: hash}, nil
	}
	for _, test := range []struct {
		name        string
		key         string
		request     json.RawMessage
		creator     identity.Subject
		fingerprint func(string) (string, error)
		find        func() (CreationRecord, error)
		want        error
		calls       []string
	}{
		{"recorded intent", "create", request, creator, fingerprints, found, nil, []string{"FindCreation tenant create"}},
		{"missing creation", "create", request, creator, fingerprints, func() (CreationRecord, error) { return CreationRecord{}, ErrNotFound }, ErrNotFound, []string{"FindCreation tenant create"}},
		{"another creator", "create", request, identity.Subject{Kind: "user", ID: "stranger"}, fingerprints, found, ErrIdempotencyConflict, []string{"FindCreation tenant create"}},
		{"no intent", "create", nil, creator, nil, nil, ErrInvalidInput, nil},
		{"invalid key", " ", request, creator, nil, nil, ErrInvalidInput, nil},
		{"invalid creator", "create", request, identity.Subject{}, nil, nil, ErrInvalidInput, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			storage := &fakeStorage{t: t, fingerprint: test.fingerprint, findCreation: test.find}
			service, err := NewService(storage, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.FindSessionCreation(t.Context(), "tenant", test.key, test.request, test.creator)
			if test.want == nil && (err != nil || result.Session.ID != "session" || result.Created) || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("result %+v, %v", result, err)
			}
			if strings.Join(storage.calls, "\n") != strings.Join(test.calls, "\n") {
				t.Fatalf("storage calls %q", storage.calls)
			}
		})
	}
}
