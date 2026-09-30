package providers

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/providerassets"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

func TestArtifactProjectionsMatchRegistrations(t *testing.T) {
	catalog, err := ArtifactCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(catalog, providerassets.Catalog()) {
		t.Fatal("stale Web artifact projection; regenerate provider-artifacts")
	}
	raw, err := os.ReadFile("../../../../../deploy/install/provider_assets.py")
	if err != nil {
		t.Fatal(err)
	}
	_, body, found := strings.Cut(string(raw), "CATALOG = json.loads(")
	if !found {
		t.Fatal("missing Python artifact projection")
	}
	encoded, _, _ := strings.Cut(body, ")\n")
	text, err := strconv.Unquote(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var python map[string][]providerassets.Artifact
	if err := json.Unmarshal([]byte(text), &python); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(catalog, python) {
		t.Fatal("stale installer artifact projection; regenerate provider-artifacts")
	}
}

func TestNodeArtifactRegistrationRejectsInvalidDeclarations(t *testing.T) {
	for _, mutate := range []func(*Adapter){
		func(a *Adapter) { a.NodeArtifacts = nil },
		func(a *Adapter) { a.NodeArtifacts[0].Path = "../private" },
		func(a *Adapter) { a.NodeArtifacts[0].Path = "." },
		func(a *Adapter) { a.NodeArtifacts[0].Path = ".." },
		func(a *Adapter) { a.NodeArtifacts[0].Suffix = ".." },
		func(a *Adapter) { a.NodeArtifacts[0].Suffix = "bad\x00name" },
		func(a *Adapter) { a.NodeArtifacts[0].Suffix = "../private" },
		func(a *Adapter) { a.NodeArtifacts[0].Role = "unknown" },
		func(a *Adapter) { a.NodeArtifacts = append(a.NodeArtifacts, a.NodeArtifacts[0]) },
	} {
		a := adapters["docker"]
		a.NodeArtifacts = append([]providerassets.Artifact(nil), a.NodeArtifacts...)
		mutate(&a)
		if err := ValidateRegistration(a); !errors.Is(err, providercontract.ErrContract) {
			t.Fatalf("invalid artifacts accepted: %v", err)
		}
	}
	const kind = "another-node-provider"
	adapters[kind] = adapters["docker"]
	defer delete(adapters, kind)
	catalog, err := ArtifactCatalog()
	if err != nil || !reflect.DeepEqual(catalog[kind], adapters[kind].NodeArtifacts) {
		t.Fatalf("additional registration not projected: %v", err)
	}
}

func TestArtifactCatalogRejectsConflictingSharedFiles(t *testing.T) {
	const kind = "conflicting-provider"
	defer delete(adapters, kind)
	for _, mutate := range []func(*providerassets.Artifact){
		func(item *providerassets.Artifact) { item.Suffix = "different" },
		func(item *providerassets.Artifact) { item.Path = "native/bin/other-node" },
	} {
		a := adapters["docker"]
		a.NodeArtifacts = append([]providerassets.Artifact(nil), a.NodeArtifacts...)
		mutate(&a.NodeArtifacts[0])
		adapters[kind] = a
		if _, err := ArtifactCatalog(); !errors.Is(err, providercontract.ErrContract) {
			t.Fatalf("conflicting declaration accepted: %v", err)
		}
	}
}
