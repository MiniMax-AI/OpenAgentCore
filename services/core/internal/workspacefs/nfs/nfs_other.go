//go:build !linux

package nfs

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

func (*Adapter) Check(context.Context) error { return workspacefs.ErrUnsupported }
func (*Adapter) Create(context.Context, workspacefs.Reference) (workspacefs.Attachment, error) {
	return workspacefs.Attachment{}, workspacefs.ErrUnsupported
}
func (*Adapter) Observe(context.Context, workspacefs.Reference) (workspacefs.Attachment, error) {
	return workspacefs.Attachment{}, workspacefs.ErrUnsupported
}
func (*Adapter) Delete(context.Context, workspacefs.Reference) error {
	return workspacefs.ErrUnsupported
}
