package microsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

type workspaceResolverFunc func(context.Context, workspacefs.Binding) (workspacefs.Directory, error)

func (f workspaceResolverFunc) Resolve(ctx context.Context, b workspacefs.Binding) (workspacefs.Directory, error) {
	return f(ctx, b)
}
func workspaceBinding() *workspacefs.Binding {
	r := testRef()
	c := workspacefs.Configuration{ID: "77777777-7777-4777-8777-777777777777", Adapter: "test", Parameters: json.RawMessage(`{}`)}
	return &workspacefs.Binding{Configuration: c, Attachment: workspacefs.Attachment{Reference: workspacefs.Reference{TenantID: r.TenantID, EnvironmentID: r.EnvironmentID, ObjectID: "88888888-8888-4888-8888-888888888888"}, ConfigurationID: c.ID, Kind: workspacefs.AttachmentHostDirectory, Native: json.RawMessage(`{}`)}}
}
func TestWorkspaceResolveBeforeRestoreAndRetainPartialTarget(t *testing.T) {
	c, r, s := testConfig(), testRef(), testSnapshot()
	c.EnvironmentDiskMiB = 0
	c.ExternalWorkspace = true
	resolves, calls := 0, 0
	p, err := NewWithCaller(c, callerFunc(func(_ context.Context, q Request) (Response, error) {
		calls++
		if q.Workspace == nil || q.Workspace.Path != "/resolved/environment" || q.Workspace.ObjectID != workspaceBinding().Attachment.Reference.ObjectID || q.Resume.Workspace != nil {
			t.Fatal("helper received unresolved binding", q)
		}
		target := q.Resume.Target
		target.ID = "local:partial"
		return Response{Version: ProtocolVersion, State: &State{Compute: target}, ErrorCode: "unconfirmed"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	p.workspace = workspaceResolverFunc(func(_ context.Context, b workspacefs.Binding) (workspacefs.Directory, error) {
		resolves++
		return workspacefs.Directory{Path: "/resolved/environment"}, nil
	})
	q := ResumeRequest{Reference: r, OperationID: "66666666-6666-4666-8666-666666666666", Snapshot: s, Target: Compute{Generation: 1, Name: Name(c, r, 1), RestoredFrom: &s}, Workspace: workspaceBinding()}
	state, err := p.Resume(deadline(t), q)
	if !errors.Is(err, ErrUnconfirmed) || state.Compute.ID != "local:partial" || resolves != 1 || calls != 1 {
		t.Fatal(state, err, resolves, calls)
	}
	q.ObserveOnly = true
	_, _ = p.Resume(deadline(t), q)
	if resolves != 2 {
		t.Fatal("restore observation did not resolve original binding")
	}
	q.Workspace.Attachment.Reference.EnvironmentID = r.TenantID
	_, err = p.Resume(deadline(t), q)
	if !errors.Is(err, workspacefs.ErrOwnership) || calls != 2 {
		t.Fatal("foreign binding reached helper", err, calls)
	}
}
func TestWorkspaceDoesNotFallbackWithoutResolver(t *testing.T) {
	calls := 0
	p, _ := NewWithCaller(testConfig(), callerFunc(func(context.Context, Request) (Response, error) { calls++; return Response{}, nil }))
	_, err := p.Create(deadline(t), sandbox.Bootstrap{Reference: testRef(), Workspace: workspaceBinding()})
	if !errors.Is(err, workspacefs.ErrUnsupported) || calls != 0 {
		t.Fatal(err, calls)
	}
}
