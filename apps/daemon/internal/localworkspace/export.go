package localworkspace

import (
	"context"
	"errors"
	"io"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (b *Binding) ExportOutputs(ctx context.Context, output io.Writer) error {
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
