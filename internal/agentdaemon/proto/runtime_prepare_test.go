package proto

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/google/uuid"
)

func capabilityBegin() RuntimePreparePayload {
	return RuntimePreparePayload{Step: "begin", EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(),
		Action: "skill", Skill: &agentskill.Metadata{Type: "inline", Name: "example", Description: "Example"}, SizeBytes: 10, SHA256: strings.Repeat("a", 64)}
}

func TestCapabilitiesRequestValidation(t *testing.T) {
	valid := capabilityBegin()
	if !ValidRuntimePrepareRequest(valid) {
		t.Fatal("valid skill refused")
	}
	for name, mutate := range map[string]func(*RuntimePreparePayload){
		"noncanonical identity": func(p *RuntimePreparePayload) { p.EnvironmentID = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA" },
		"zero identity":         func(p *RuntimePreparePayload) { p.SessionID = uuid.Nil.String() },
		"begin data":            func(p *RuntimePreparePayload) { p.Data = []byte("x") },
		"begin offset":          func(p *RuntimePreparePayload) { p.Offset = 1 },
		"mixed metadata": func(p *RuntimePreparePayload) {
			p.Plugin = &agentplugin.Metadata{Type: "inline", Name: "example", Description: "Example"}
		},
		"mixed sources":    func(p *RuntimePreparePayload) { p.Sources = &agentcapabilities.Input{} },
		"missing metadata": func(p *RuntimePreparePayload) { p.Skill = nil },
		"wrong metadata type": func(p *RuntimePreparePayload) {
			p.Skill = &agentskill.Metadata{Type: "reference", Name: "example", Description: "Example"}
		},
		"missing description": func(p *RuntimePreparePayload) { p.Skill = &agentskill.Metadata{Type: "inline", Name: "example"} },
		"oversized header": func(p *RuntimePreparePayload) {
			p.Skill = &agentskill.Metadata{Type: "inline", Name: "example", Description: strings.Repeat("x", RuntimePrepareMaxFrameBytes)}
		},
		"skill slot":        func(p *RuntimePreparePayload) { p.Slot = 1 },
		"no digest":         func(p *RuntimePreparePayload) { p.SHA256 = "" },
		"uppercase digest":  func(p *RuntimePreparePayload) { p.SHA256 = strings.ToUpper(p.SHA256) },
		"short digest":      func(p *RuntimePreparePayload) { p.SHA256 = "aa" },
		"no archive":        func(p *RuntimePreparePayload) { p.SizeBytes = 0 },
		"oversized archive": func(p *RuntimePreparePayload) { p.SizeBytes = RuntimePrepareMaxBytes + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			p := valid
			mutate(&p)
			if ValidRuntimePrepareRequest(p) {
				t.Fatal("invalid begin accepted")
			}
		})
	}
	plugin := valid
	plugin.Action, plugin.Skill, plugin.Plugin, plugin.Slot = "plugin", nil, &agentplugin.Metadata{Type: "inline", Name: "example", Description: "Example"}, 49
	if !ValidRuntimePrepareRequest(plugin) {
		t.Fatal("valid plugin refused")
	}
	plugin.Slot = 50
	if ValidRuntimePrepareRequest(plugin) {
		t.Fatal("out of range slot accepted")
	}
	for _, p := range []RuntimePreparePayload{
		{Step: "chunk", Offset: 0, Data: []byte("x")},
		{Step: "chunk", Offset: RuntimePrepareMaxBytes - RuntimePrepareChunkBytes, Data: make([]byte, RuntimePrepareChunkBytes)},
		{Step: "commit"},
	} {
		if !ValidRuntimePrepareRequest(p) {
			t.Fatal("valid continuation refused", p.Step)
		}
	}
	for _, p := range []RuntimePreparePayload{
		{Step: "chunk", Data: []byte("x"), Action: "skill"},
		{Step: "chunk", Data: []byte("x"), EnvironmentID: valid.EnvironmentID},
		{Step: "chunk", Data: []byte("x"), Skill: valid.Skill},
		{Step: "chunk", Data: []byte("x"), Offset: -1},
		{Step: "chunk", Data: []byte("x"), Offset: RuntimePrepareMaxBytes},
		{Step: "chunk", Data: make([]byte, RuntimePrepareChunkBytes+1)},
		{Step: "chunk"},
		{Step: "commit", Offset: 1},
		{Step: "commit", Data: []byte("x")},
		{Step: "commit", Sources: &agentcapabilities.Input{}},
	} {
		if ValidRuntimePrepareRequest(p) {
			t.Fatal("invalid continuation accepted", p.Step)
		}
	}
}

func TestCapabilitiesFinalizeRequiresExplicitBoundedSelection(t *testing.T) {
	p := RuntimePreparePayload{Step: "begin", EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(), Action: "finalize", Sources: &agentcapabilities.Input{}}
	if !ValidRuntimePrepareRequest(p) {
		t.Fatal("explicit empty finalization refused")
	}
	for _, mutate := range []func(*RuntimePreparePayload){
		func(p *RuntimePreparePayload) { p.Sources = nil },
		func(p *RuntimePreparePayload) { p.SHA256 = strings.Repeat("a", 64) },
		func(p *RuntimePreparePayload) { p.SizeBytes = 1 },
		func(p *RuntimePreparePayload) {
			p.Sources = &agentcapabilities.Input{Directories: make([]string, 51)}
		},
		func(p *RuntimePreparePayload) {
			p.Sources = &agentcapabilities.Input{Directories: []string{"/workspace/../secret"}}
		},
		func(p *RuntimePreparePayload) {
			p.Sources = &agentcapabilities.Input{Directories: []string{"/workspace/a", "/workspace/a"}}
		},
	} {
		bad := p
		mutate(&bad)
		if ValidRuntimePrepareRequest(bad) {
			t.Fatal("invalid finalization accepted")
		}
	}
}

func TestCapabilitiesResultCannotMisrepresentUncertainty(t *testing.T) {
	for _, r := range []RuntimePrepareResultPayload{
		{Outcome: "rejected", ErrorCode: "runtime_preparation_unconfirmed"},
		{Outcome: "unknown", ErrorCode: "runtime_preparation_rejected"},
		{Outcome: "failed", ErrorCode: "private detail"},
		{Outcome: "rejected", ErrorCode: "invalid_request", Offset: 1},
		{Outcome: "unknown", ErrorCode: "runtime_preparation_unconfirmed", SizeBytes: 10},
		{Outcome: "completed", SizeBytes: 10},
		{Outcome: "ready", Offset: 1},
		{Outcome: "ready", SizeBytes: 10},
		{Outcome: "ready", ErrorCode: "invalid_request"},
	} {
		if ValidRuntimePrepareResult(r, "ready", 0, 10) {
			t.Fatal("unsafe receipt accepted", r)
		}
	}
	if ValidRuntimePrepareResult(RuntimePrepareResultPayload{Outcome: "received", Offset: 1}, "received", 2, 10) {
		t.Fatal("wrong offset accepted")
	}
	if ValidRuntimePrepareResult(RuntimePrepareResultPayload{Outcome: "completed", SizeBytes: 9}, "completed", 0, 10) {
		t.Fatal("wrong size accepted")
	}
	for _, r := range []RuntimePrepareResultPayload{
		{Outcome: "rejected", ErrorCode: "invalid_request"},
		{Outcome: "failed", ErrorCode: "runtime_preparation_failed"},
		{Outcome: "unknown", ErrorCode: "runtime_preparation_unconfirmed"},
		{Outcome: "completed", SizeBytes: 10},
	} {
		if !ValidRuntimePrepareResult(r, "completed", 0, 10) {
			t.Fatal("safe receipt refused", r)
		}
	}
}

func TestLocalEnvironmentCarriesSelectionButNeverInstalledRoots(t *testing.T) {
	input := &agentcapabilities.Input{Directories: []string{"/workspace/capabilities"}}
	original := LocalEnvironment{ID: uuid.NewString(), WorkspaceDirectory: "/workspace", CapabilitySources: input, Capabilities: true,
		Skills: []agentcapabilities.InstalledSkill{{RelativeRoot: "private-installed-root"}},
		MCP:    []EnvironmentMCP{{PackageRoot: "private-mcp-root"}}}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-") {
		t.Fatal("installed roots leaked to wire")
	}
	var decoded LocalEnvironment
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != original.ID || decoded.WorkspaceDirectory != "/workspace" || !reflect.DeepEqual(decoded.CapabilitySources, input) || !decoded.Capabilities || len(decoded.Skills) != 0 || len(decoded.MCP) != 0 {
		t.Fatal("selection round trip changed", decoded)
	}
}

func TestRuntimePreparationInitialActions(t *testing.T) {
	base := RuntimePreparePayload{Step: "begin", EnvironmentID: uuid.NewString(), SessionID: uuid.NewString()}
	for _, initialization := range []RuntimeInitialization{
		{Action: "configure", Env: map[string]string{"EXAMPLE": "value"}},
		{Action: "npm", Network: "disabled", Packages: []string{"typescript"}},
		{Action: "python", Network: "enabled", Packages: []string{"requests"}},
		{Action: "setup", Network: "enabled", Command: "echo done"},
		{Action: "setup", Network: "disabled", Command: "echo done", CWD: "/workspace/project"},
	} {
		p := base
		p.Action, p.Initialization = "initialize", &initialization
		if !ValidRuntimePrepareRequest(p) {
			t.Fatalf("valid initialization refused: %s", initialization.Action)
		}
		for _, mutate := range []func(*RuntimePreparePayload){
			func(p *RuntimePreparePayload) { p.File = &RuntimeInitialFile{Path: "/workspace/a"} },
			func(p *RuntimePreparePayload) { p.SizeBytes = 1 },
			func(p *RuntimePreparePayload) { p.SHA256 = strings.Repeat("a", 64) },
			func(p *RuntimePreparePayload) { p.Data = []byte("x") },
		} {
			bad := p
			mutate(&bad)
			if ValidRuntimePrepareRequest(bad) {
				t.Fatal("mixed initialization accepted")
			}
		}
	}
	for _, initialization := range []RuntimeInitialization{
		{Action: "exec", Command: "echo done"},
		{Action: "configure", Packages: []string{"git"}},
		{Action: "configure", Env: map[string]string{"A=B": "value"}},
		{Action: "system", Network: "enabled", Packages: []string{"git"}},
		{Action: "npm", Network: "invalid", Packages: []string{"a"}},
		{Action: "python", Network: "enabled", Packages: []string{"--help"}},
		{Action: "setup", Network: "enabled", Command: "x", CWD: "/environment"},
		{Action: "setup", Network: "enabled", Command: "x", CWD: "/workspace/../private"},
		{Action: "setup", Network: "enabled", Command: "x", Env: map[string]string{}},
		{Action: "setup", Network: "enabled", Command: "x", Packages: []string{}},
	} {
		p := base
		p.Action, p.Initialization = "initialize", &initialization
		if ValidRuntimePrepareRequest(p) {
			t.Fatalf("invalid initialization accepted: %+v", initialization)
		}
	}
	p := base
	p.Action, p.File, p.SHA256 = "file", &RuntimeInitialFile{Path: "/workspace/empty"}, strings.Repeat("a", 64)
	if !ValidRuntimePrepareRequest(p) {
		t.Fatal("empty initial file refused")
	}
	for _, name := range []string{"/workspace", "/workspace/../secret", "/workspace/a/", "/environment/file", `C:\workspace\file`, "/workspace/a\\b"} {
		bad := p
		bad.File = &RuntimeInitialFile{Path: name}
		if ValidRuntimePrepareRequest(bad) {
			t.Fatal("invalid logical file accepted", name)
		}
	}
	for _, p := range []RuntimePreparePayload{{Step: "commit", File: &RuntimeInitialFile{Path: "/workspace/a"}}, {Step: "chunk", Data: []byte("a"), Initialization: &RuntimeInitialization{Action: "configure"}}} {
		if ValidRuntimePrepareRequest(p) {
			t.Fatal("continuation metadata accepted")
		}
	}
}

func TestRuntimePreparationExitCodes(t *testing.T) {
	for _, code := range []int{0, 1, 255} {
		if !ValidRuntimePrepareResult(RuntimePrepareResultPayload{Outcome: "failed", ErrorCode: "runtime_preparation_failed", ExitCode: code}, "ready", 0, 0) {
			t.Fatal("valid exit refused", code)
		}
	}
	for _, outcome := range []string{"ready", "received", "completed", "rejected", "unknown", "failed"} {
		for _, code := range []int{-1, 256, 1} {
			if outcome == "failed" && code == 1 {
				continue
			}
			r := RuntimePrepareResultPayload{Outcome: outcome, ExitCode: code}
			switch outcome {
			case "rejected":
				r.ErrorCode = "invalid_request"
			case "unknown":
				r.ErrorCode = "runtime_preparation_unconfirmed"
			case "failed":
				r.ErrorCode = "runtime_preparation_failed"
			}
			if ValidRuntimePrepareResult(r, outcome, 0, 0) {
				t.Fatal("invalid exit combination", r)
			}
		}
	}
}

func TestRuntimePreparationPortableSources(t *testing.T) {
	p := RuntimePreparePayload{Step: "begin", EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(), Action: "finalize", Sources: &agentcapabilities.Input{Directories: []string{`C:\Users\operator\skills`, `\\host\share\skills`}}}
	if !ValidRuntimePrepareRequest(p) {
		t.Fatal("portable sources rejected by wire")
	}
	if agentcapabilities.ValidateLocalDirectories(p.Sources.Directories) == nil {
		t.Fatal("Windows sources accepted by Linux resolver")
	}
}
