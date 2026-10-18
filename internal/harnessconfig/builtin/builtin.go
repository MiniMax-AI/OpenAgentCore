// Package builtin is the set of Harnesses in this build. The registry itself is
// generated from the packages under internal/harnessconfig that hold a
// declaration.go.
package builtin

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"

func Registry() harnessconfig.Registry { return registry }

// Kinds returns the public identifiers in this build, not Runtime availability.
func Kinds() []string { return registry.Kinds() }

func Contains(kind string) bool {
	_, ok := registry.Lookup(kind)
	return ok
}
