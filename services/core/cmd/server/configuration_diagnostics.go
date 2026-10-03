package main

import (
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

// reason must be authored validation text, never a parser or dependency error.
func configurationFailure(setting, reason string, cause error) error {
	fields := []any{"setting", setting, "reason", reason}
	if cause != nil {
		fields = append(fields, log.ErrorFields(cause)...)
	}
	log.Bg().Error("Core configuration invalid", fields...)
	message := setting + ": " + reason
	if cause != nil {
		return fmt.Errorf("%s: %w", message, cause)
	}
	return errors.New(message)
}
