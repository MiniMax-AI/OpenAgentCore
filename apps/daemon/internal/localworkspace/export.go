package localworkspace

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"io"
)

func (b *Binding) CanExport() bool { return b != nil && b.workspace != "" }
func (b *Binding) ExportOutputs(ctx context.Context, output io.Writer) error {
	if !b.CanExport() || output == nil {
		return errors.New("workspace export unavailable")
	}
	return b.exportNativeOutputs(ctx, &exportWriter{output: output})
}

type exportWriter struct {
	output io.Writer
	size   int64
}

func (w *exportWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if int64(len(data)) > proto.WorkspaceExportMaxBytes-w.size {
		return 0, errors.New("workspace export exceeds bound")
	}
	n, err := w.output.Write(data)
	w.size += int64(n)
	return n, err
}
