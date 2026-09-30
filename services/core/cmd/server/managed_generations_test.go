package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type routingSetupStore struct {
	setupStore
	old   store.SandboxSetup
	oldID string
}

func (s *routingSetupStore) GetSandboxAllocationSetup(_ context.Context, ref sandbox.Reference) (store.SandboxSetup, error) {
	value := s.value
	if ref.AllocationID == s.oldID {
		value = s.old
		key := *value.Configuration.(*e2b.DeploymentConfiguration)
		key.APIKey = s.value.Configuration.(*e2b.DeploymentConfiguration).APIKey
		value.Configuration = &key
	}
	return value, nil
}
func TestE2BRouterKeepsOldSpecificationWithCommittedCredential(t *testing.T) {
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "helper")
	script := `#!/usr/bin/env python3
import json,sys,pathlib
q=json.load(sys.stdin)
with (pathlib.Path(q['Config']['StateDir'])/'requests').open('a') as f: f.write(json.dumps(q)+'\n')
info=dict(q['Reference'],State='running',ProviderID='owned',CreateSettled=True)
if q['Operation']=='kill': info['State']='absent'
print(json.dumps({'Version':1,'Info':info}))
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_E2B_PROVIDER_BIN", helper)
	t.Setenv("OAC_E2B_STATE_DIR", state)
	id := uuid.NewString()
	old := store.SandboxSetup{InstallationID: id, Provider: "e2b", Mode: "direct", Generation: 1, Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}}, Configuration: &e2b.DeploymentConfiguration{APIKey: "old-key", Template: "old:" + uuid.NewString()}}
	current := old
	current.Generation = 2
	current.Specification.Resources.CPUs = 4
	current.Configuration = &e2b.DeploymentConfiguration{APIKey: "new-key", Template: "new:" + uuid.NewString()}
	ref := sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
	db := &routingSetupStore{setupStore: setupStore{value: current}, old: old, oldID: ref.AllocationID}
	setup := &managedSetup{store: db, installationID: id}
	// A facade retained by a generation-one lifecycle still reads current credentials.
	router := &generationRouter{setup: setup, store: db}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := router.GetInfo(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Renew(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := router.Kill(ctx, ref); err != nil {
		t.Fatal(err)
	}
	next := ref
	next.AllocationID = uuid.NewString()
	if _, err := router.GetInfo(ctx, next); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(state, "requests"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 4 {
		t.Fatal(len(lines))
	}
	for i, line := range lines {
		var q struct {
			Config struct {
				APIKey, Template string
				Resources        sandbox.Resources
			}
		}
		if err := json.Unmarshal([]byte(line), &q); err != nil {
			t.Fatal(err)
		}
		expected := old
		if i == 3 {
			expected = current
		}
		if q.Config.APIKey != "new-key" || q.Config.Template != expected.Configuration.(*e2b.DeploymentConfiguration).Template || q.Config.Resources != expected.Specification.Resources {
			t.Fatal("generation or credential mismatch", i)
		}
	}
}

// The empty resource set must not let a publicly readable template substitute
// for a team identity. In particular, the old key must be checked as itself.
func TestE2BReplacementRequiresCommittedOwnershipAnchor(t *testing.T) {
	for _, tc := range []struct {
		name, committedKey, committedTemplate, candidateKey, candidateTemplate string
		want                                                                   error
		reset                                                                  bool
	}{
		{name: "legacy public template cross team", committedKey: "team-a", committedTemplate: "public-b", candidateKey: "team-b", candidateTemplate: "public-b", reset: true},
		{name: "revoked committed key", committedKey: "revoked", committedTemplate: "owned-a", candidateKey: "team-a", candidateTemplate: "owned-a", reset: true},
		{name: "unknown committed ownership", committedKey: "unconfirmed", committedTemplate: "owned-a", candidateKey: "team-a", candidateTemplate: "owned-a", want: sandbox.ErrConfigurationUnconfirmed},
		{name: "proven different team", committedKey: "team-a", committedTemplate: "owned-a", candidateKey: "team-b", candidateTemplate: "public-b", want: sandbox.ErrCredentialOwnership},
		{name: "same team replacement", committedKey: "team-a", committedTemplate: "owned-a", candidateKey: "team-a-rotated", candidateTemplate: "new-a"},
		{name: "explicit same key", committedKey: "team-a", committedTemplate: "owned-a", candidateKey: "team-a", candidateTemplate: "owned-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := t.TempDir()
			if err := os.Chmod(state, 0700); err != nil {
				t.Fatal(err)
			}
			helper := filepath.Join(t.TempDir(), "provider")
			const script = `#!/usr/bin/env python3
import json, pathlib, sys
q = json.load(sys.stdin)
k, template = q['Config']['APIKey'], q['Config']['Template'].split(':')[0]
with (pathlib.Path(q['Config']['StateDir']) / 'requests').open('a') as log:
    log.write(json.dumps(q) + '\n')
code = ''
if k == 'revoked': code = 'unauthorized'
elif k == 'unconfirmed': code = 'unconfirmed'
elif not (k.startswith('team-a') and template in ('owned-a', 'new-a') or k == 'team-b' and template == 'public-b'): code = 'team_mismatch'
result = {'Version': 1, 'ErrorCode': code}
if not code:
    result['DeploymentValid'] = True
    if q['Operation'] == 'validate_deployment':
        result['TemplateBuild'] = {'Status': 'ready', 'CPUs': 2, 'MemoryMiB': 2048}
print(json.dumps(result))
`
			if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("OAC_E2B_PROVIDER_BIN", helper)
			t.Setenv("OAC_E2B_STATE_DIR", state)
			id, build := uuid.NewString(), ":"+uuid.NewString()
			current := store.SandboxSetup{InstallationID: id, Provider: "e2b", Mode: "direct", Generation: 1,
				Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048}},
				Configuration: &e2b.DeploymentConfiguration{APIKey: tc.committedKey, Template: tc.committedTemplate + build}}
			db := &setupStore{value: current}
			s := &managedSetup{installationID: id, store: db}
			loaded, err := s.load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			next := current
			next.Configuration = &e2b.DeploymentConfiguration{APIKey: tc.candidateKey, Template: tc.candidateTemplate + build}
			candidate, err := s.prepare(t.Context(), next)
			if err != nil {
				t.Fatal(err)
			}
			err = candidate.VerifyCredential(t.Context())
			var reset *store.SandboxResetRequiredError
			if tc.reset {
				if !errors.As(err, &reset) || errors.Is(err, sandbox.ErrCredentialRejected) || errors.Is(err, sandbox.ErrCredentialOwnership) {
					t.Fatalf("unanchored ownership misattributed: %v", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("got %v; want %v", err, tc.want)
			}
			if db.value.Generation != 1 || db.value.Configuration.(*e2b.DeploymentConfiguration).APIKey != tc.committedKey || s.selected.Load().Config != loaded {
				t.Fatal("verification mutated committed selection")
			}
			raw, err := os.ReadFile(filepath.Join(state, "requests"))
			if err != nil {
				t.Fatal(err)
			}
			var requests []e2b.Request
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var q e2b.Request
				if err := json.Unmarshal([]byte(line), &q); err != nil {
					t.Fatal(err)
				}
				requests = append(requests, q)
				if q.Operation != "verify_credential" && q.Operation != "validate_deployment" {
					t.Fatal("verification mutated provider", q.Operation)
				}
			}
			if len(requests) < 2 || requests[1].Config.APIKey != tc.committedKey || requests[1].Config.Template != current.Configuration.(*e2b.DeploymentConfiguration).Template {
				t.Fatal("committed key was replaced before establishing ownership")
			}
			if tc.reset || tc.want == sandbox.ErrConfigurationUnconfirmed {
				if len(requests) != 2 {
					t.Fatal("unanchored current ownership reached candidate verification")
				}
			}
		})
	}
}
