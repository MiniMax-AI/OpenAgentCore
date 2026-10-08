package codex

import (
	"context"
	"os"
	"path/filepath"
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

func TestReadOnlyPreparationRejectedBeforeNativeSetup(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	req.WorkspaceReadOnly = true
	if e, err := newExecutor(t.Context(), req, cfg); err == nil || e != nil {
		t.Fatal("read-only request admitted", err)
	}
	if len(preparationFrames(t, root)) != 0 {
		t.Fatal("read-only request started native child")
	}
	if _, err := os.Stat(filepath.Join(root, "home", viewCodexHome)); !os.IsNotExist(err) {
		t.Fatal("read-only request created native state", err)
	}
}
