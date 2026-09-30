package main

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
)

func TestProjectionMatchesEveryAdapterDeclaration(t *testing.T) {
	got := declarations()
	if len(got) != len(builtin.Kinds()) {
		t.Fatal("projection membership differs from catalog")
	}
	for _, kind := range builtin.Kinds() {
		configuration := builtin.Configuration(kind)
		providers, ok := got[kind]
		if !ok || len(providers) != len(configuration.Providers) {
			t.Fatalf("%s: missing provider declarations", kind)
		}
		for _, declaration := range configuration.Providers {
			projected, ok := providers[declaration.Protocol]
			if !ok || projected.RequiresTokenLimits != declaration.RequiresTokenLimits {
				t.Fatalf("%s/%s: configuration rule differs", kind, declaration.Protocol)
			}
		}
	}
}
