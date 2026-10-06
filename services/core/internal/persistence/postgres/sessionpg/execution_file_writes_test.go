package sessionpg

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestFileWriteSettlementNeedsAStoredWrite(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	operations, _ := sessionExecution(t, pool)
	tenantID, _, environmentID := newEnvironment(t, pool, "self_hosted", "connected")
	tenant, environment := uuidText(tenantID), uuidText(environmentID)
	key := sessions.FileWriteIdentity{ID: uuid.NewString(), DeviceID: uuid.NewString(), RequestSHA256: strings.Repeat("a", 64)}
	for _, test := range []struct {
		name, tenant, environment string
		want                      error
	}{
		{"an unknown write", tenant, environment, sessions.ErrNotFound},
		{"another tenant's Environment", uuid.NewString(), environment, sessions.ErrNotFound},
		{"a malformed Environment", tenant, "environment", sessions.ErrInvalidInput},
	} {
		if _, err := operations.SettleEnvironmentFileWrite(t.Context(), test.tenant, test.environment, key, "committed"); !errors.Is(err, test.want) {
			t.Fatalf("%s: %v, want %v", test.name, err, test.want)
		}
	}
}
