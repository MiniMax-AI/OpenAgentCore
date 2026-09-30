//go:build unix

package claudesdk

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExecutorRejectsNonNativeFrozenProviderBeforeStartup(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			config, req := persistentConfig(t, "complete")
			provider := map[string]any{"protocol": protocol, "base_url": "https://model.invalid/v1", "api_key": "private-sentinel"}
			req.AgentOptions["model_provider"] = provider
			resource, err := NewExecutorFactory(config)(t.Context(), req)
			if resource != nil || err == nil || !strings.Contains(err.Error(), "does not support") || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatalf("non-native provider acquired native ownership: %v", err)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(config.Entrypoint), "probes")); !os.IsNotExist(err) {
				t.Fatal("unsupported snapshot reached native readiness")
			}
			if !reflect.DeepEqual(provider, map[string]any{"protocol": protocol, "base_url": "https://model.invalid/v1", "api_key": "private-sentinel"}) {
				t.Fatal("frozen provider was rewritten")
			}
		})
	}
}
