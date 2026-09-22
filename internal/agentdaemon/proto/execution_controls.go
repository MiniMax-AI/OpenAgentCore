package proto

import "errors"

// ValidateProgrammaticToolCallingDisable applies only to explicit tool disabling.
// Omission preserves the adapter's ordinary native behavior.
func (r PromptRequestPayload) ValidateProgrammaticToolCallingDisable(supported bool) error {
	if r.ExecutionControls != nil && r.ExecutionControls.DisableProgrammaticToolCalling && !supported {
		return errors.New("engine does not support disabling programmatic tool calling")
	}
	return nil
}
