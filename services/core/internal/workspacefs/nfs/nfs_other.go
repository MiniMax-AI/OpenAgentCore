//go:build !linux

package nfs

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

func (*Filesystem) Check(context.Context) error { return workspacefs.ErrUnsupported }
func (*Filesystem) Create(context.Context, workspacefs.Reference, uint32) (workspacefs.Attachment, error) {
	return workspacefs.Attachment{}, workspacefs.ErrUnsupported
}
func (*Filesystem) Observe(context.Context, workspacefs.Reference) (workspacefs.Attachment, error) {
	return workspacefs.Attachment{}, workspacefs.ErrUnsupported
}
func (*Filesystem) Delete(context.Context, workspacefs.Reference) error {
	return workspacefs.ErrUnsupported
}
