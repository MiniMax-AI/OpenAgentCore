package mcode

import (
	"encoding/json"
	"fmt"
)

// ValidateWorkspaceReadiness checks the installed companion's private contract.
// Neither upstream version alone nor a successful CLI --version proves cleanup.
func ValidateWorkspaceReadiness(raw []byte) error {
	var info struct {
		Protocol       int `json:"protocol"`
		Native, Source string
	}
	if len(raw) > 4096 || json.Unmarshal(raw, &info) != nil || info.Protocol != 2 || info.Native != SupportedVersion || info.Source != "33b259bbbeb1c16433390869938191d09bdb0680" {
		return fmt.Errorf("mcode: workspace companion check failed")
	}
	return nil
}
