package runtimebootstrap

import (
	_ "embed"
	"encoding/json"
)

// suspensionJSON is also consumed by the E2B template packager. Keep hosted
// startup and recovery on the same private control-file location.
//
//go:embed suspension.json
var suspensionJSON []byte

var suspendControlFile = func() string {
	var configuration struct {
		ControlFile string `json:"control_file"`
	}
	if err := json.Unmarshal(suspensionJSON, &configuration); err != nil {
		panic(err)
	}
	return configuration.ControlFile
}()

// SuspendControlFile is the packaged hosted Runtime protocol default, not a
// user-editable deployment setting. Providers prepare its private parent directory.
func SuspendControlFile() string { return suspendControlFile }
