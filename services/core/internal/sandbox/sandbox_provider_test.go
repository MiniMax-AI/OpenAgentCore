package sandbox

import (
	"reflect"
	"slices"
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
	groups := slices.Concat(requiredOperations, suspensionOperations, []string{"Observe"})
	slices.Sort(groups)
	if !slices.Equal(methods, groups) {
		t.Fatalf("SandboxProvider operations %v, groups %v", methods, groups)
	}
}
