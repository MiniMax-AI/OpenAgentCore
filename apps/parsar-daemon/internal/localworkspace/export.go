package localworkspace

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (b *Binding) CanExport() bool { return b != nil && b.exportHelper != "" }

// ExportOutputs streams only from the deployment-owned root; successful exit is mandatory.
func (b *Binding) ExportOutputs(ctx context.Context, output io.Writer) error {
	if !b.CanExport() || output == nil {
		return errors.New("workspace export unavailable")
	}
	cmd := exec.CommandContext(ctx, b.exportHelper, b.workspace)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd.WaitDelay = time.Second
	stdout, childOutput, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stdout.Close()
	defer childOutput.Close()
	cmd.Stdout = childOutput
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = childOutput.Close()
	stop := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer stop()
	exited := make(chan struct{})
	settled := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		// Bound a pending read when a descendant inherits stdout after helper exit.
		_ = stdout.SetReadDeadline(time.Now().Add(time.Second))
		close(exited)
		settled <- err
	}()
	// Own the pipe so exec.Wait cannot close buffered output during transport
	// backpressure. Only actual pipe reads have a post-exit cleanup deadline.
	_, copyErr := io.Copy(&exportWriter{output: output}, exportReader{stdout, exited})
	if copyErr != nil {
		_ = cmd.Process.Kill()
	}
	return errors.Join(copyErr, <-settled)
}

type exportReader struct {
	pipe   *os.File
	exited <-chan struct{}
}

func (r exportReader) Read(data []byte) (int, error) {
	select {
	case <-r.exited:
		// Consumer delays do not consume the inherited-pipe cleanup allowance.
		if err := r.pipe.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return 0, err
		}
	default:
	}
	return r.pipe.Read(data)
}

type exportWriter struct {
	output io.Writer
	size   int64
}

func (w *exportWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > proto.WorkspaceExportMaxBytes-w.size {
		return 0, errors.New("workspace export exceeds bound")
	}
	n, err := w.output.Write(data)
	w.size += int64(n)
	return n, err
}
