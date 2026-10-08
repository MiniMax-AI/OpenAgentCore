package mcode

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// A successful public real-model run supplies an actual foreign native ID.
// Neither that history nor a missing ID may prepare an Executor, so no Turn can
// run against it or silently replace it with a new session.
func TestNativeMCodeHistoryIsolation(t *testing.T) {
	options, foreign := os.Getenv("OAC_TEST_MCODE_REAL_OPTIONS"), os.Getenv("OAC_TEST_MCODE_FOREIGN_NATIVE_ID")
	if options == "" || foreign == "" {
		t.Skip("private provider options and foreign history ID required")
	}
	install := installedView(t)
	raw, err := os.ReadFile(options)
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"foreign": foreign, "missing": "00000000-0000-4000-8000-000000000000"} {
		t.Run(name, func(t *testing.T) {
			req := testRequest(t)
			if json.Unmarshal(raw, &req) != nil {
				t.Fatal("invalid private options")
			}
			req.AgentSessionID = id
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			e, err := prepareExecutor(t, ctx, install, req)
			if e != nil || err == nil || !strings.Contains(err.Error(), "session/load:") || strings.Contains(err.Error(), "deadline exceeded") {
				t.Fatal("native history was not explicitly rejected")
			}
		})
	}
}
