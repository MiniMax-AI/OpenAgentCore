package execution

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestExistingSessionRecoveryRequiresVerifiedCapability(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk"} {
		for _, started := range []bool{false, true} {
			for _, nativeID := range []string{"", "native"} {
				for _, capable := range []bool{false, true} {
					wantRecovery := started && nativeID == ""
					req, err := (&Dispatcher{SessionsReader: frozenProvider{engine: engine}}).executionRequest(t.Context(), sessions.Session{ID: "session", Engine: engine}, Snapshot{}, proto.Declaration{Capabilities: proto.AgentKindCapabilities{NativeSessionRecovery: proto.CapabilityFromBool(capable)}}, sessions.ExecutionBinding{HasStartedTurn: started, NativeSessionID: nativeID})
					if wantRecovery && !capable {
						if err == nil {
							t.Fatal("unverified recovery admitted", engine)
						}
						continue
					}
					if err != nil || req.RequireExistingNativeSession != wantRecovery || req.AgentSessionID != nativeID {
						t.Fatalf("engine=%s started=%v id=%s capability=%v request=%+v err=%v", engine, started, nativeID, capable, req, err)
					}
				}
			}
		}
	}
}
