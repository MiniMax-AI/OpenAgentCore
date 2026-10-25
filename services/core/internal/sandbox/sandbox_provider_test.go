package sandbox

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The operation groups partition SandboxProvider's operations, so a new method
// cannot escape the required or all-or-nothing checkpoint checks.
func TestOperationGroupsPartitionTheInterface(t *testing.T) {
	contract := reflect.TypeFor[SandboxProvider]()
	var methods []string
	for i := range contract.NumMethod() {
		if name := contract.Method(i).Name; name != "ProviderOperations" {
			methods = append(methods, name)
		}
	}
	groups := slices.Concat(requiredOperations, checkpointOperations, preparationOperations, []string{"Observe"})
	slices.Sort(groups)
	if !slices.Equal(methods, groups) {
		t.Fatalf("SandboxProvider operations %v, groups %v", methods, groups)
	}
}

func TestSnapshotEvidenceIsBounded(t *testing.T) {
	s := SnapshotIdentity{Compatibility: CheckpointCompatibility{ArtifactDomain: "domain", ExecutionClass: "class"}, Evidence: strings.Repeat("e", MaxSnapshotEvidenceBytes)}
	if s.Validate() != nil {
		t.Fatal("bounded evidence rejected")
	}
	s.Evidence += "e"
	if s.Validate() == nil {
		t.Fatal("oversized evidence accepted")
	}
}
