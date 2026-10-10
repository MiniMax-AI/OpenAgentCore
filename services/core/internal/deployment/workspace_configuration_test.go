package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/google/uuid"
)

func TestWorkspaceConfigurationUsesStoredGenerationWithoutNativeProvider(t *testing.T) {
	declaration := workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}
	for _, provider := range []string{"", "docker", "microsandbox"} {
		t.Run(provider, func(t *testing.T) {
			record := Record{InstallationID: uuid.NewString(), Specification: []byte(`{}`)}
			if provider != "" {
				record = webDeployment(t, uuid.NewString(), provider, 1)
			}
			service := newService(t, &fakeStorage{t: t}, &fakeReader{t: t, deployment: func(context.Context) (Record, error) { return record, nil }}, testPublicURL)
			err := service.ValidateWorkspaceConfiguration(t.Context(), declaration)
			if provider == "docker" {
				if !errors.Is(err, workspacefs.ErrUnsupported) {
					t.Fatal("incompatible provider accepted", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkspaceConfigurationRejectsChangedExternalReceiptAndReadFailure(t *testing.T) {
	declaration := workspacefs.Declaration{Attachment: workspacefs.AttachmentHostDirectory, UserXAttr: true}
	record := webDeployment(t, uuid.NewString(), "microsandbox", 1)
	specification := testSpecification("microsandbox")
	specification.Resources.EnvironmentDiskMiB = 0
	specification.Workspace = &declaration
	record.Specification, _ = json.Marshal(specification)
	var readErr error
	service := newService(t, &fakeStorage{t: t}, &fakeReader{t: t, deployment: func(context.Context) (Record, error) { return record, readErr }}, testPublicURL)
	if err := service.ValidateWorkspaceConfiguration(t.Context(), declaration); err != nil {
		t.Fatal(err)
	}
	changed := declaration
	changed.CapacityQuota = true
	if err := service.ValidateWorkspaceConfiguration(t.Context(), changed); !errors.Is(err, workspacefs.ErrUnsupported) {
		t.Fatal("changed receipt accepted", err)
	}
	readErr = errors.New("database unavailable")
	if err := service.ValidateWorkspaceConfiguration(t.Context(), declaration); !errors.Is(err, readErr) {
		t.Fatal("unreadable setup treated as unconfigured", err)
	}
}
