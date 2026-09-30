package sandbox

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// This contract owns numeric bounds, release patterns and canonical field order.
// Python installers consume its generated projection in node_spec.py.
const minimumDiskMiB uint32 = 1024

type resourceRule struct {
	Name     string `json:"name"`
	Min      uint32 `json:"min"`
	Max      uint32 `json:"max"`
	OmitZero bool   `json:"omit_zero"`
	Message  string `json:"-"`
}
type runtimeRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// DeploymentPolicy is declared by an adapter and projected to the installer.
type DeploymentPolicy struct {
	RuntimeError string `json:"-"`
	Disk         bool   `json:"disk"`
	Runtime      bool   `json:"runtime"`
}

var resourceContract = []resourceRule{
	{"cpus", 1, 255, false, "cpus must be 1..255 and memory_mib must be 512..1048576"},
	{"memory_mib", 512, 1048576, false, "cpus must be 1..255 and memory_mib must be 512..1048576"},
	{"root_disk_mib", 0, ^uint32(0), true, ""},
	{"environment_disk_mib", 0, ^uint32(0), true, ""},
}
var runtimeContract = []runtimeRule{
	{"source_commit", "[0-9a-f]{40}"},
	{"image_id", "sha256:[0-9a-f]{64}"},
	{"image_manifest_digest", "sha256:[0-9a-f]{64}"},
	{"microsandbox_ref", "oac-runtime@sha256:[0-9a-f]{64}"},
	{"runtime_sha256", "[0-9a-f]{64}"},
	{"firmware_sha256", "[0-9a-f]{64}"},
}

// PythonDeploymentContract generates the installer projection. Struct order is
// checked before generation because it also defines Go's canonical JSON bytes.
func PythonDeploymentContract(policies map[string]DeploymentPolicy) string {
	for _, item := range []struct {
		value any
		names []string
	}{
		{Resources{}, resourceNames()}, {RuntimeRelease{}, runtimeNames()},
	} {
		typ := reflect.TypeOf(item.value)
		if typ.NumField() != len(item.names) {
			panic("deployment contract field count differs")
		}
		for i, name := range item.names {
			if strings.Split(typ.Field(i).Tag.Get("json"), ",")[0] != name {
				panic("deployment contract field order differs")
			}
		}
	}
	raw, _ := json.Marshal(struct {
		Resources   []resourceRule              `json:"resources"`
		Runtime     []runtimeRule               `json:"runtime"`
		Providers   map[string]DeploymentPolicy `json:"providers"`
		MinimumDisk uint32                      `json:"minimum_disk"`
	}{resourceContract, runtimeContract, policies, minimumDiskMiB})
	return "# BEGIN GENERATED DEPLOYMENT CONTRACT\n# Generated from sandbox/deployment_contract.go; do not edit.\n_CONTRACT = json.loads(" + fmt.Sprintf("%q", string(raw)) + ")\n# END GENERATED DEPLOYMENT CONTRACT"
}
func resourceNames() []string {
	var names []string
	for _, r := range resourceContract {
		names = append(names, r.Name)
	}
	return names
}
func runtimeNames() []string {
	var names []string
	for _, r := range runtimeContract {
		names = append(names, r.Name)
	}
	return names
}
