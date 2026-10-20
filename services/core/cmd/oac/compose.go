package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes docker compose in the installation directory. Tests substitute it.
type Runner interface {
	Run(ctx context.Context, args ...string) error
	Output(ctx context.Context, args ...string) ([]byte, error)
}

type execRunner struct{ dir, project string }

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
	prefix := []string{"compose"}
	if r.project != "" {
		prefix = append(prefix, "-p", r.project)
	}
	cmd := exec.CommandContext(ctx, "docker", append(prefix, args...)...)
	cmd.Dir = r.dir
	cmd.Env = dockerEnv()
	return cmd
}

// dockerEnv is the environment of every Docker child. The installation's .env is the one home of OAC_* and COMPOSE_* settings, so the caller's shell cannot override them.
func dockerEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if !strings.HasPrefix(name, "OAC_") && !strings.HasPrefix(name, "COMPOSE_") {
			env = append(env, entry)
		}
	}
	return env
}
