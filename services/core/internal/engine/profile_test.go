package engine_test

import (
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine/enginetest"
)

func TestCatalogOwnsQualification(t *testing.T) {
	placements := []string{"none"}
	profiles := map[string]engine.Profile{"fixture": enginetest.Profile(func(p *engine.Profile) { p.Placements = placements })}
	catalog := engine.NewCatalog(profiles)
	placements[0] = "openai_hosted"
	profiles["fixture"] = engine.Profile{MCPBearer: proto.CapabilitySupported}
	profiles["unqualified"] = enginetest.Profile(nil)
	profile, ok := catalog.Lookup("fixture")
	if !ok || !profile.Accepts("none") || profile.Accepts("openai_hosted") || profile.MCPBearer.IsSupported() {
		t.Fatal("caller changed catalog qualification")
	}
	profile.Placements[0] = "self_hosted"
	profile.MCPBearer = proto.CapabilitySupported
	again, _ := catalog.Lookup("fixture")
	if !again.Accepts("none") || again.MCPBearer.IsSupported() {
		t.Fatal("lookup exposed mutable qualification")
	}
	if _, ok := catalog.Lookup("unqualified"); ok {
		t.Fatal("caller registered an engine after catalog construction")
	}
	if _, ok := (engine.Catalog{}).Lookup("fixture"); ok {
		t.Fatal("fixture widened the service catalog")
	}
}

func TestCatalogKindsAreStableAndDefensive(t *testing.T) {
	catalog := engine.NewCatalog(map[string]engine.Profile{"zeta": enginetest.Profile(nil), "alpha": enginetest.Profile(nil)})
	got := catalog.Kinds()
	if !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("Kinds = %#v", got)
	}
	got[0] = "changed"
	if again := catalog.Kinds(); !reflect.DeepEqual(again, []string{"alpha", "zeta"}) {
		t.Fatalf("caller mutated catalog kinds: %#v", again)
	}
}

func TestExplicitCatalogDoesNotInheritBuiltins(t *testing.T) {
	for _, catalog := range []engine.Catalog{engine.NewCatalog(nil), engine.NewCatalog(map[string]engine.Profile{"fixture": enginetest.Profile(nil)})} {
		if _, ok := catalog.Lookup("codex"); ok {
			t.Fatal("explicit catalog inherited built-in qualification")
		}
	}
	if _, ok := (engine.Catalog{}).Lookup("codex"); !ok {
		t.Fatal("zero-value catalog lost built-in qualification")
	}
}
