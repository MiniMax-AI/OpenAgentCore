package engine_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine/enginetest"
)

func requireDeclarationRejection(t *testing.T, p engine.Profile) {
	t.Helper()
	if err := p.ValidateDeclaration(); !errors.Is(err, engine.ErrInvalidDeclaration) {
		t.Fatalf("expected declaration error, got %v", err)
	}
	defer func() {
		err, ok := recover().(error)
		if !ok || !errors.Is(err, engine.ErrInvalidDeclaration) {
			t.Fatalf("catalog accepted invalid declaration: %v", err)
		}
	}()
	engine.NewCatalog(map[string]engine.Profile{"fixture": p})
}

func TestEveryProfileFieldRequiresAnExplicitDecision(t *testing.T) {
	// Claude exercises all three additional validators. Zeroing any field, even
	// a future one, must fail before the profile enters an immutable catalog.
	original, _ := (engine.Catalog{}).Lookup("claude_sdk")
	typ := reflect.TypeOf(original)
	for i := 0; i < typ.NumField(); i++ {
		t.Run(typ.Field(i).Name, func(t *testing.T) {
			p := original
			reflect.ValueOf(&p).Elem().Field(i).SetZero()
			requireDeclarationRejection(t, p)
		})
	}
}

func TestEveryServiceCapabilityRejectsInvalidValues(t *testing.T) {
	for _, kind := range (engine.Catalog{}).Kinds() {
		original, _ := (engine.Catalog{}).Lookup(kind)
		if err := original.ValidateDeclaration(); err != nil {
			t.Fatal(kind, err)
		}
		typ := reflect.TypeOf(original)
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).Type != reflect.TypeOf(proto.CapabilitySupport(0)) {
				continue
			}
			for _, invalid := range []proto.CapabilitySupport{proto.CapabilityUnspecified, 3, 255} {
				t.Run(kind+"/"+typ.Field(i).Name+"/"+strconv.Itoa(int(invalid)), func(t *testing.T) {
					p := original
					reflect.ValueOf(&p).Elem().Field(i).Set(reflect.ValueOf(invalid))
					requireDeclarationRejection(t, p)
				})
			}
		}
	}
}

func TestValidationPoliciesMustMatchCallbacks(t *testing.T) {
	original, _ := (engine.Catalog{}).Lookup("claude_sdk")
	for _, field := range []string{"ConfigurationValidation", "ToolsValidation", "FunctionResultValidation"} {
		for _, invalid := range []engine.ValidationPolicy{engine.ValidationUnspecified, 3, engine.CommonValidationOnly} {
			t.Run(field+"/"+strconv.Itoa(int(invalid)), func(t *testing.T) {
				p := original
				reflect.ValueOf(&p).Elem().FieldByName(field).Set(reflect.ValueOf(invalid))
				requireDeclarationRejection(t, p)
			})
		}
		t.Run(field+"/missing callback", func(t *testing.T) {
			p := enginetest.Profile(nil)
			reflect.ValueOf(&p).Elem().FieldByName(field).Set(reflect.ValueOf(engine.AdditionalValidation))
			requireDeclarationRejection(t, p)
		})
	}
	// Common validation is sufficient when explicitly chosen, with no pretend
	// successful callbacks. An empty MCP list is an explicit refusal of all origins.
	if err := enginetest.Profile(nil).ValidateDeclaration(); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogRejectsInvalidCombinationsBeforeCallbacks(t *testing.T) {
	cases := map[string]func(*engine.Profile){
		"empty placements":              func(p *engine.Profile) { p.Placements = []string{} },
		"unknown placement":             func(p *engine.Profile) { p.Placements = []string{"private-fixture-value"} },
		"duplicate placement":           func(p *engine.Profile) { p.Placements = []string{"none", "none"} },
		"unknown origin":                func(p *engine.Profile) { p.MCPOrigins = []string{"private-fixture-value"} },
		"duplicate origin":              func(p *engine.Profile) { p.MCPOrigins = []string{"service", "service"} },
		"service without placement":     func(p *engine.Profile) { p.Placements = []string{"self_hosted"}; p.MCPOrigins = []string{"service"} },
		"environment without placement": func(p *engine.Profile) { p.MCPOrigins = []string{"environment"} },
		"bearer without MCP":            func(p *engine.Profile) { p.MCPBearer = proto.CapabilitySupported },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			p := enginetest.Profile(change)
			p.ConfigurationValidation = engine.AdditionalValidation
			p.ValidateConfiguration = func(v1.Agent, *v1.Environment) error {
				t.Fatal("registration invoked request validator")
				return nil
			}
			if err := p.ValidateDeclaration(); err != nil && strings.Contains(err.Error(), "private-fixture-value") {
				t.Fatal("declaration error disclosed input")
			}
			requireDeclarationRejection(t, p)
		})
	}
}

func TestCatalogRejectsEmptyProfileAndKind(t *testing.T) {
	requireDeclarationRejection(t, engine.Profile{})
	for _, kind := range []string{"", " ", " fixture"} {
		t.Run("kind/"+kind, func(t *testing.T) {
			defer func() {
				if err, ok := recover().(error); !ok || !errors.Is(err, engine.ErrInvalidDeclaration) {
					t.Fatal("invalid kind accepted", err)
				}
			}()
			engine.NewCatalog(map[string]engine.Profile{kind: enginetest.Profile(nil)})
		})
	}
	for _, profiles := range []map[string]engine.Profile{nil, {}} {
		c := engine.NewCatalog(profiles)
		if len(c.Kinds()) != 0 {
			t.Fatal("empty catalog inherited builtins")
		}
		for _, kind := range (engine.Catalog{}).Kinds() {
			if _, ok := c.Lookup(kind); ok {
				t.Fatal("empty catalog authorized", kind)
			}
		}
	}
}
