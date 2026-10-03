package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Runner executes docker compose in the installation directory. Tests substitute it.
type Runner interface {
	Run(ctx context.Context, args ...string) error
	Output(ctx context.Context, args ...string) ([]byte, error)
}

type execRunner struct{ dir string }

func (r execRunner) Run(ctx context.Context, args ...string) error {
	cmd := r.command(ctx, args)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose %s failed", args[0])
	}
	return nil
}

func (r execRunner) Output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := r.command(ctx, args)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker compose %s failed", args[0])
	}
	return stdout.Bytes(), nil
}

func (r execRunner) command(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	cmd.Dir = r.dir
	cmd.Env = os.Environ()
	return cmd
}
