//go:build unix

package claudesdk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestExecutorRejectsNonNativeFrozenProviderBeforeStartup(t *testing.T) {
	for _, protocol := range []modelprovider.Protocol{modelprovider.Responses, modelprovider.ChatCompletions} {
		t.Run(string(protocol), func(t *testing.T) {
			config, req := persistentConfig(t, "complete")
			req.ModelProvider = &modelprovider.Provider{Protocol: protocol, BaseURL: "https://model.invalid/v1", APIKey: "private-sentinel"}
			resource, err := NewExecutorFactory(config)(t.Context(), req)
			if resource != nil || err == nil || !strings.Contains(err.Error(), "does not support") || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatalf("non-native provider acquired native ownership: %v", err)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(config.Entrypoint), "probes")); !os.IsNotExist(err) {
				t.Fatal("unsupported snapshot reached native readiness")
			}
		})
	}
}
