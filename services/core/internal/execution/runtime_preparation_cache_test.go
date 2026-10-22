package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

type preparationProvider struct {
	sandbox.SandboxProvider
	hit     bool
	saveErr error
	saved   []string
}

func (p *preparationProvider) RestorePreparation(context.Context, sandbox.Reference, string) (bool, error) {
	return p.hit, nil
}
func (p *preparationProvider) SavePreparation(_ context.Context, _ sandbox.Reference, key string) error {
	p.saved = append(p.saved, key)
	return p.saveErr
}

func TestPreparationCacheHitFinalizesMissPreparesAndSaves(t *testing.T) {
	for _, test := range []struct {
		name                string
		hit                 bool
		saveErr             error
		prepared, finalized int
		saved               int
	}{
		{"hit", true, nil, 0, 1, 0},
		{"miss", false, nil, 1, 0, 1},
		{"failed save", false, errors.New("disk full"), 1, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &preparationProvider{hit: test.hit, saveErr: test.saveErr}
			var prepared, finalized int
			cache := &preparationCache{provider: provider, key: "key"}
			err := cache.run(context.Background(), func() error { prepared++; return nil }, func() error { finalized++; return nil })
			if err != nil || prepared != test.prepared || finalized != test.finalized || len(provider.saved) != test.saved {
				t.Fatal(err, prepared, finalized, provider.saved)
			}
		})
	}
}
