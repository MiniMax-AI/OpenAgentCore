package daemonize

import (
	"context"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const spawnTestChildEnv = "OAC_TEST_DAEMON_SPAWN_CHILD"

func TestMain(m *testing.M) {
	if mode := os.Getenv(spawnTestChildEnv); mode != "" {
		_, firstStop := NotifyContext(context.Background())
		firstStop()
		ctx, stop := NotifyContext(context.Background())
		defer stop()
		fmt.Fprintln(os.Stdout, "spawn-test-child:stdout")
		fmt.Fprintln(os.Stderr, "spawn-test-child:stderr")
		if mode == "ignore" {
			time.Sleep(15 * time.Second)
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(15 * time.Second):
		}
		return
	}
	os.Exit(m.Run())
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "private")
	if err := runtimefs.EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}
