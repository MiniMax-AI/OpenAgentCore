package versionprobe

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// Config describes an adapter's CLI version command and error contract.
type Config struct {
	Name          string
	DefaultBinary string
	// Args precede --version, for a CLI started through an interpreter.
	Args           []string
	MissingError   error
	TrimBinary     bool
	StderrFallback bool
}

// Check runs `<binary> [args...] --version` and returns its trimmed first output line.
func Check(ctx context.Context, binary string, config Config) (string, error) {
	if (config.TrimBinary && strings.TrimSpace(binary) == "") || (!config.TrimBinary && binary == "") {
		binary = config.DefaultBinary
	}
	if _, err := exec.LookPath(binary); err != nil {
		return "", fmt.Errorf("%w: %s", config.MissingError, binary)
	}

	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(ctx, binary, append(slices.Clone(config.Args), "--version")...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("%s --version failed: %s", config.Name, message)
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" && config.StderrFallback {
		output = strings.TrimSpace(stderr.String())
	}
	if index := strings.IndexByte(output, '\n'); index >= 0 {
		output = output[:index]
	}
	if output == "" {
		return "", fmt.Errorf("%s --version returned empty output", config.Name)
	}
	return output, nil
}
