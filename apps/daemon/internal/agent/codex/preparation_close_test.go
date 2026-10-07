package codex

import (
	"context"
	"testing"
	"time"
)

func TestPreparationCancellationDuringReadiness(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	t.Setenv("OAC_TEST_PREPARATION_BLOCK", "1")
	owner, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		e, err := newExecutor(owner, req, cfg)
		if e != nil {
			_ = e.Close(context.Background())
		}
		result <- err
	}()
	waitPreparationMethod(t, root, "environment/status")
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled readiness succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancelled readiness did not finish")
	}
	if len(preparedCatalogs(t, root)) != 0 {
		t.Fatal("failed readiness leaked model catalog")
	}
	assertPreparationOnly(t, root)
}
