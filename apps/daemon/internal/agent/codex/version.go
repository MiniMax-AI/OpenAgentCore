package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/binpath"
	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

// InstallURL points operators at the Codex install instructions when
// the daemon can see the adapter but not the CLI binary.
const InstallURL = "https://github.com/openai/codex"

// defaultBinary is the executable to probe and spawn: binpath.Codex()
// honours the OAC_RUNTIME_CODEX_BIN override so a bare-name PATH lookup can
// be bypassed in images where PATH is not under our control. A function
// rather than a const so the env is read at call time.
func defaultBinary() string { return binpath.Codex() }

// ErrCLINotFound is returned by CheckCLIAvailable when the binary
// cannot be located on PATH. Callers use errors.Is to distinguish an
// install problem from a present-but-broken CLI.
var ErrCLINotFound = errors.New("codex CLI not found")

// CheckCLIAvailable runs `<binary> --version` and returns the trimmed
// first line. The empty binary name defaults to defaultBinary(). Matches
// the CLI availability check signature
// so connect.go's preflight loop treats every engine uniformly.
func CheckCLIAvailable(ctx context.Context, binary string) (string, error) {
	if strings.TrimSpace(binary) == "" {
		binary = defaultBinary()
	}
	if _, lookErr := exec.LookPath(binary); lookErr != nil {
		return "", fmt.Errorf("%w: %s", ErrCLINotFound, binary)
	}

	var stdout firstOutputBuffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	spawnAt := time.Now()
	err := cmd.Start()
	status := "ok"
	if err != nil {
		status = "error"
	}
	obslog.Ctx(ctx).Info("runtime version probe", "harness_kind", "codex", "stage", "process_spawn", "duration_ms", float64(time.Since(spawnAt))/float64(time.Millisecond), "status", status)
	if err == nil {
		waitAt := time.Now()
		err = cmd.Wait()
		completedAt := time.Now()
		if err != nil {
			status = "error"
		}
		obslog.Ctx(ctx).Info("runtime version probe", "harness_kind", "codex", "stage", "process_wait", "duration_ms", float64(completedAt.Sub(waitAt))/float64(time.Millisecond), "status", status)
		// Wait joins the stdout copier. Inspect its timestamp only after it returns;
		// receiving bytes never replaces process exit or output validation.
		if !stdout.first.IsZero() {
			obslog.Ctx(ctx).Info("runtime version probe", "harness_kind", "codex", "stage", "first_stdout", "duration_ms", float64(stdout.first.Sub(spawnAt))/float64(time.Millisecond))
			obslog.Ctx(ctx).Info("runtime version probe", "harness_kind", "codex", "stage", "stdout_to_completion", "duration_ms", float64(completedAt.Sub(stdout.first))/float64(time.Millisecond), "status", status)
		}
		if cmd.ProcessState != nil {
			obslog.Ctx(ctx).Info("runtime version probe resources", "harness_kind", "codex",
				"user_cpu_ms", float64(cmd.ProcessState.UserTime())/float64(time.Millisecond),
				"system_cpu_ms", float64(cmd.ProcessState.SystemTime())/float64(time.Millisecond))
		}
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("codex --version failed: %s", msg)
	}
	out := strings.TrimSpace(stdout.String())
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	if out == "" {
		return "", fmt.Errorf("codex --version returned empty output")
	}
	return out, nil
}

// exec writes through a single stdout copier and Wait joins it. Do not embed
// bytes.Buffer: its ReaderFrom would bypass Write and lose the first-byte event.
type firstOutputBuffer struct {
	buffer bytes.Buffer
	first  time.Time
}

func (b *firstOutputBuffer) Write(p []byte) (int, error) {
	if len(p) > 0 && b.first.IsZero() {
		b.first = time.Now()
	}
	return b.buffer.Write(p)
}

func (b *firstOutputBuffer) String() string { return b.buffer.String() }
