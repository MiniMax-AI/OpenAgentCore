package placement

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

const publicURL = "https://core.example"

// fakeDeclarations serves the declarations a test sets; a declaration the
// test left nil fails the test.
type fakeDeclarations struct {
	t                     *testing.T
	requiresPublicOrigin  func(provider string) (bool, error)
	validateSpecification func(provider string, spec sandbox.DeploymentSpec) error
}

func (f *fakeDeclarations) RequiresPublicOrigin(provider string) (bool, error) {
	if f.requiresPublicOrigin == nil {
		f.t.Fatalf("unexpected RequiresPublicOrigin(%q)", provider)
	}
	return f.requiresPublicOrigin(provider)
}

func (f *fakeDeclarations) ValidateSpecification(provider string, spec sandbox.DeploymentSpec) error {
	if f.validateSpecification == nil {
		f.t.Fatalf("unexpected ValidateSpecification(%q)", provider)
	}
	return f.validateSpecification(provider, spec)
}

func rules(t *testing.T, declarations Declarations, url string) *Rules {
	t.Helper()
	r, err := NewRules(declarations, url)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func generation(value uint64) *uint64 { return &value }

func TestNewRulesRequiresDeclarations(t *testing.T) {
	if _, err := NewRules(nil, publicURL); err == nil {
		t.Fatal("NewRules accepted nil declarations")
	}
	if got := rules(t, &fakeDeclarations{t: t}, publicURL).PublicURL(); got != publicURL {
		t.Fatalf("PublicURL = %q", got)
	}
}

// The built-in registry satisfies the declarations the rules read.
func TestRegistryDeclaresPublicOrigin(t *testing.T) {
	loopback := rules(t, providers.Builtin(), "http://127.0.0.1:8091")
	if err := loopback.CheckPublicOrigin("e2b"); !errors.Is(err, ErrPublicURLUnreachable) {
		t.Fatalf("CheckPublicOrigin(e2b) on loopback = %v", err)
	}
	if err := loopback.CheckPublicOrigin("docker"); err != nil {
		t.Fatalf("CheckPublicOrigin(docker) on loopback = %v", err)
	}
	if err := rules(t, providers.Builtin(), publicURL).CheckPublicOrigin("e2b"); err != nil {
		t.Fatalf("CheckPublicOrigin(e2b) on a public URL = %v", err)
	}
	if err := loopback.CheckPublicOrigin("unknown"); err == nil {
		t.Fatal("CheckPublicOrigin accepted an unknown provider")
	}
}

func TestCheckAdmission(t *testing.T) {
	installation := uuid.NewString()
	invalid := errors.New("invalid specification")
	declarations := &fakeDeclarations{t: t, validateSpecification: func(provider string, spec sandbox.DeploymentSpec) error {
		if spec.Resources.CPUs != 2 {
			return invalid
		}
		return nil
	}}
	valid := Deployment{InstallationID: installation, Provider: "docker", Specification: json.RawMessage(`{"resources":{"cpus":2}}`)}
	with := func(change func(*Deployment)) Deployment {
		d := valid
		change(&d)
		return d
	}
	for name, test := range map[string]struct {
		d            Deployment
		installation string
		want         error
		message      string
	}{
		"unclaimed admits everything": {Deployment{Resetting: true, AdmissionPaused: true}, installation, nil, ""},
		"admitted":                    {valid, installation, nil, ""},
		"new Session":                 {valid, "", nil, ""},
		"reset":                       {with(func(d *Deployment) { d.Resetting, d.AdmissionPaused = true, true }), installation, ErrResetAdmission, "hosted admission is paused for a sandbox reset"},
		"paused":                      {with(func(d *Deployment) { d.AdmissionPaused = true }), installation, ErrAdmissionClosed, "environment is no longer available: sandbox creation is paused for provider maintenance"},
		"malformed specification":     {with(func(d *Deployment) { d.Specification = json.RawMessage(`[`) }), installation, ErrAdmissionClosed, "environment is no longer available: sandbox creation requires a deployment specification"},
		"rejected specification":      {with(func(d *Deployment) { d.Specification = json.RawMessage(`{"resources":{"cpus":3}}`) }), installation, ErrAdmissionClosed, "environment is no longer available: sandbox creation requires a deployment specification"},
		"no provider":                 {with(func(d *Deployment) { d.Provider, d.Specification = "", json.RawMessage(`[`) }), installation, nil, ""},
		"other installation":          {valid, uuid.NewString(), ErrAdmissionClosed, "environment is no longer available: sandbox installation does not match deployment"},
	} {
		err := rules(t, declarations, publicURL).CheckAdmission(test.d, test.installation)
		if !errors.Is(err, test.want) || (test.want == nil) != (err == nil) || (err != nil && err.Error() != test.message) {
			t.Errorf("%s: CheckAdmission = %v, want %v with %q", name, err, test.want, test.message)
		}
	}
}

func TestDecidePlacement(t *testing.T) {
	origin := func(required bool) *fakeDeclarations {
		return &fakeDeclarations{t: t, requiresPublicOrigin: func(string) (bool, error) { return required, nil }}
	}
	ready := func(id string, g uint64, active int64) Node {
		return Node{ID: id, Online: true, ServingReady: true, ReadyGeneration: generation(g), Active: active, MaxActive: 4, Retained: active, MaxRetained: 4, CoreURL: publicURL}
	}
	nodes := Deployment{Provider: "docker", Mode: "nodes", WebManaged: true}
	with := func(change func(*Deployment)) Deployment {
		d := nodes
		change(&d)
		return d
	}
	offline, unready, full, retainedFull, elsewhere := ready("offline", 9, 0), ready("unready", 9, 0), ready("full", 9, 4), ready("retained", 9, 0), ready("elsewhere", 9, 0)
	offline.Online = false
	unready.ServingReady = false
	retainedFull.Retained = 4
	elsewhere.CoreURL = "https://other.example"
	preparing := Node{ID: "preparing", Online: true, TargetState: "preparing", MaxActive: 1, MaxRetained: 1, CoreURL: publicURL}
	for name, test := range map[string]struct {
		declarations *fakeDeclarations
		url          string
		d            Deployment
		nodes        []Node
		want         *Placement
		err          error
	}{
		"reset":                       {origin(false), publicURL, with(func(d *Deployment) { d.Resetting = true }), nil, nil, ErrResetAdmission},
		"loopback public origin":      {origin(true), "http://localhost:8091", nodes, []Node{ready("a", 1, 0)}, nil, ErrPublicURLUnreachable},
		"direct":                      {origin(false), publicURL, with(func(d *Deployment) { d.Mode = "direct" }), nil, nil, nil},
		"direct paused":               {origin(false), publicURL, with(func(d *Deployment) { d.Mode, d.AdmissionPaused = "direct", true }), nil, nil, ErrNodeUnavailable},
		"no provider":                 {&fakeDeclarations{t: t}, publicURL, Deployment{}, nil, nil, nil},
		"no provider on Web":          {&fakeDeclarations{t: t}, publicURL, Deployment{WebManaged: true}, nil, nil, ErrNodeUnavailable},
		"paused":                      {origin(false), publicURL, with(func(d *Deployment) { d.AdmissionPaused = true }), []Node{ready("a", 1, 0)}, nil, ErrNodeUnavailable},
		"no nodes":                    {origin(false), publicURL, nodes, nil, nil, ErrNodeUnavailable},
		"only ineligible nodes":       {origin(false), publicURL, nodes, []Node{offline, unready, full, retainedFull, elsewhere, {ID: "never", Online: true, ServingReady: true, MaxActive: 1, MaxRetained: 1, CoreURL: publicURL}}, nil, ErrNodeUnavailable},
		"preparing":                   {origin(false), publicURL, nodes, []Node{offline, preparing}, nil, ErrNodesPreparing},
		"highest generation":          {origin(false), publicURL, nodes, []Node{ready("old", 1, 0), ready("new", 2, 3), preparing}, &Placement{NodeID: "new", Generation: 2}, nil},
		"fewest active":               {origin(false), publicURL, nodes, []Node{ready("busy", 2, 3), ready("idle", 2, 1)}, &Placement{NodeID: "idle", Generation: 2}, nil},
		"other address off Web":       {origin(false), publicURL, with(func(d *Deployment) { d.WebManaged = false }), []Node{elsewhere}, &Placement{NodeID: "elsewhere", Generation: 9}, nil},
		"public origin on public URL": {origin(true), publicURL, nodes, []Node{ready("a", 1, 0)}, &Placement{NodeID: "a", Generation: 1}, nil},
	} {
		got, err := rules(t, test.declarations, test.url).DecidePlacement(test.d, test.nodes)
		if !errors.Is(err, test.err) || (test.err == nil) != (err == nil) || (got == nil) != (test.want == nil) || (got != nil && *got != *test.want) {
			t.Errorf("%s: DecidePlacement = %+v, %v; want %+v, %v", name, got, err, test.want, test.err)
		}
	}
}

func TestCheckReservedAndRestore(t *testing.T) {
	for name, test := range map[string]struct {
		reserved Reserved
		want     error
	}{
		"available":   {Reserved{NodeID: "a", Generation: 1, Available: true}, nil},
		"released":    {Reserved{NodeID: "a", Generation: 1, Released: true, Available: true}, ErrNodeUnavailable},
		"unavailable": {Reserved{NodeID: "a", Generation: 1}, ErrNodeUnavailable},
	} {
		if err := CheckReserved(test.reserved); !errors.Is(err, test.want) || (test.want == nil) != (err == nil) {
			t.Errorf("%s: CheckReserved = %v, want %v", name, err, test.want)
		}
	}
	node := func(online bool, active int64) *Node {
		return &Node{ID: "a", Online: online, Active: active, MaxActive: 2}
	}
	for name, test := range map[string]struct {
		restore Restore
		want    error
	}{
		"ready":              {Restore{Node: node(true, 1), Generation: 3, GenerationReady: true}, nil},
		"missing node":       {Restore{Generation: 3, GenerationReady: true}, ErrNodeUnavailable},
		"no generation":      {Restore{Node: node(true, 1), GenerationReady: true}, ErrNodeUnavailable},
		"offline":            {Restore{Node: node(false, 1), Generation: 3, GenerationReady: true}, ErrNodeUnavailable},
		"generation unready": {Restore{Node: node(true, 1), Generation: 3}, ErrNodeUnavailable},
		"at capacity":        {Restore{Node: node(true, 2), Generation: 3, GenerationReady: true}, ErrNodeUnavailable},
	} {
		if err := CheckRestore(test.restore); !errors.Is(err, test.want) || (test.want == nil) != (err == nil) {
			t.Errorf("%s: CheckRestore = %v, want %v", name, err, test.want)
		}
	}
}

func TestLoopbackOrigin(t *testing.T) {
	for value, want := range map[string]bool{
		"http://localhost:8091": true, "http://127.0.0.1:8091": true, "http://127.0.0.2": true, "http://[::1]:8091": true,
		"https://core.example": false, "https://[2001:db8::1]": false, "https://10.0.0.1": false, "": false, "http://host.localhost": false,
	} {
		if got := LoopbackOrigin(value); got != want {
			t.Errorf("LoopbackOrigin(%q) = %v, want %v", value, got, want)
		}
	}
}
