package proto

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/google/uuid"
)

func TestAssignmentBindWorkspace(t *testing.T) {
	environment := uuid.NewString()
	for name, test := range map[string]struct {
		workspace string
		valid     bool
	}{
		"hosted":         {"/workspace", true},
		"self hosted":    {"/projects/my project", true},
		"root":           {"/", true},
		"drive syntax":   {`C:\projects`, true},
		"UNC syntax":     {`\\host\share`, true},
		"missing":        {"", false},
		"relative":       {"projects", false},
		"parent":         {"/projects/../other", false},
		"trailing slash": {"/projects/", false},
		"backslash":      {`/projects\other`, false},
		"control":        {"/projects\tother", false},
		"invalid UTF-8":  {"/projects/\xff", false},
		"oversized":      {"/" + strings.Repeat("a", 4096), false},
	} {
		t.Run(name, func(t *testing.T) {
			bind := AssignmentBindPayload{EnvironmentID: environment, WorkspaceDirectory: test.workspace}
			if err := bind.Validate(); (err == nil) != test.valid {
				t.Fatalf("Validate() = %v, want valid %t", err, test.valid)
			}
		})
	}
	if err := (AssignmentBindPayload{}).Validate(); err != nil {
		t.Fatalf("none: %v", err)
	}
	if err := (AssignmentBindPayload{WorkspaceDirectory: "/workspace"}).Validate(); err == nil {
		t.Fatal("none accepted a workspace")
	}
	resource := &sandboxbootstrap.Resource{TenantID: uuid.NewString(), EnvironmentID: environment, Kind: "allocation", ID: uuid.NewString(), Generation: 1}
	bind := AssignmentBindPayload{EnvironmentID: environment, WorkspaceDirectory: "/projects/custom", Resource: resource, AttachGrant: []byte("grant")}
	if err := bind.Validate(); err != nil {
		t.Fatalf("Link bind: %v", err)
	}
	bind.EnvironmentID, bind.WorkspaceDirectory = "", ""
	if err := bind.Validate(); err == nil {
		t.Fatal("none accepted an Environment resource")
	}
}
