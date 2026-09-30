package installroot

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"io"
	"strings"
	"time"
)

// Native diagnostics are deliberately discarded: dependencies may echo their
// environment. Readiness is separate from model credentials and a live Turn.
func Probe(parent context.Context, binary string, args, env []string, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	p, err := clirunner.Start(clirunner.StartOptions{Parent: ctx, Binary: binary, Args: args, Env: env, Dir: dir, OwnProcessGroup: true, KillTimeout: 250 * time.Millisecond})
	if err != nil {
		return "", errors.New("native component failed to start")
	}
	defer p.Cancel()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, p.Stderr); close(done) }()
	raw, err := io.ReadAll(io.LimitReader(p.Stdout, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		p.Cancel()
	}
	_, _ = io.Copy(io.Discard, p.Stdout)
	<-done
	waitErr := p.Wait()
	if err != nil || waitErr != nil || ctx.Err() != nil || len(raw) > 64*1024 {
		return "", errors.New("native component compatibility check failed")
	}
	return strings.TrimSpace(string(raw)), nil
}
