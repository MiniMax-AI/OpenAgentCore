package main

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
)

func TestProjectionMatchesEveryDeclaration(t *testing.T) {
	got := declarations()
	if len(got) != len(builtin.Kinds()) {
		t.Fatal("projection membership differs from the registry")
	}
	for _, projected := range got {
		declaration, ok := builtin.Registry().Lookup(projected.Kind)
		if !ok || projected.Label != declaration.Label || len(projected.Protocols) != len(declaration.Providers) {
			t.Fatalf("%s: projection differs from its declaration", projected.Kind)
		}
		for i, declared := range declaration.Providers {
			if projected.Protocols[i] != declared.Protocol {
				t.Fatalf("%s: protocol order differs", projected.Kind)
			}
		}
	}
}
