package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteOnlyConfiguration(t *testing.T) {
	c := Config{InstallationID: "94be54a1-138c-4f30-bc87-b13686272dbe", Provider: "microsandbox", Nodes: &Nodes{Local: false, MaxActive: 4, MaxRetained: 16, IdleSeconds: 300, RetentionSeconds: 86400}}
	b, close, err := Build(c)
	defer close()
	if err != nil {
		t.Fatal(err)
	}
	if b.Provider != nil || b.Suspension == nil || b.Suspension.IdleTimeout != 5*time.Minute {
		t.Fatal("remote-only Core constructed local compute or lost idle policy")
	}
	first := b.BackendFingerprint
	c.Nodes.MaxActive = 5
	changed, close, err := Build(c)
	defer close()
	if err != nil || changed.BackendFingerprint != first {
		t.Fatal("capacity changed deployment identity", err)
	}
	c.Microsandbox = &Microsandbox{}
	if _, close, err := Build(c); err == nil {
		close()
		t.Fatal("remote-only Core accepted local backend")
	}
	c.Microsandbox = nil
	c.Provider = "docker"
	if _, close, err := Build(c); err == nil {
		close()
		t.Fatal("Docker accepted a snapshot policy")
	}
	c.Nodes.IdleSeconds = 0
	c.Nodes.RetentionSeconds = 0
	b, close, err = Build(c)
	defer close()
	if err != nil || b.Suspension != nil || b.BackendFingerprint == first {
		t.Fatal("provider kind not fenced", err)
	}
}

func TestNodeConfigurationMustBeExplicit(t *testing.T) {
	for _, fragment := range []string{`null`, `{}`, `{"local":null}`, `{"local":false,"unexpected":true}`} {
		t.Run(fragment, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			raw := `{"installation_id":"94be54a1-138c-4f30-bc87-b13686272dbe","provider":"docker","nodes":` + fragment + `}`
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("ambiguous configuration accepted")
			}
		})
	}
	c := Config{InstallationID: "94be54a1-138c-4f30-bc87-b13686272dbe", Provider: "docker", Nodes: &Nodes{MaxActive: 2, MaxRetained: 1}}
	if _, close, err := Build(c); err == nil {
		close()
		t.Fatal("unbounded node capacity accepted")
	}
	c.Provider = "other"
	c.Nodes.MaxRetained = 4
	if _, close, err := Build(c); err == nil {
		close()
		t.Fatal("unknown provider accepted")
	}
	if strings.EqualFold(BackendFingerprint("docker", "a"), BackendFingerprint("microsandbox", "a")) {
		t.Fatal("provider namespaces collide")
	}
}
