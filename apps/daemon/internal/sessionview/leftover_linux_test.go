//go:build linux

package sessionview

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestEndLeftoverViewsEndsLiveViews checks that EndLeftoverViews ends a running view with every process in it and leaves a process that only shares the launcher's command line.
func TestEndLeftoverViewsEndsLiveViews(t *testing.T) {
	requireView(t)
	f := newFixture(t)
	w := &loopbackWorld{dir: f.world}
	token := fmt.Sprintf("oac-leftover-%d", time.Now().UnixNano())
	v, err := Start(context.Background(), f.spec(w, "wait", "OAC_VIEW_TOKEN="+token))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer v.Close()
	if line, err := bufio.NewReader(v.Stdout()).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("harness said %q, %v", line, err)
	}
	if n := processesWith(t, token); n != 1 {
		t.Fatalf("%d grandchildren in the view, want 1", n)
	}
	// It has the launcher's command line but is no PID 1 of a view.
	other := exec.Command("/bin/sh")
	other.Args = []string{launcherArg0}
	other.Stdin = strings.NewReader("sleep 60\n")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		other.Process.Kill()
		other.Wait()
	}()

	if err := EndLeftoverViews(5 * time.Second); err != nil {
		t.Fatalf("EndLeftoverViews: %v", err)
	}
	waited := make(chan struct{})
	go func() {
		v.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the view still runs")
	}
	if n := processesWith(t, token); n != 0 {
		t.Errorf("%d processes of the view survived", n)
	}
	// A killed child stays a zombie until other.Wait reaps it.
	if status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", other.Process.Pid)); err != nil || strings.Contains(string(status), "State:\tZ") {
		t.Errorf("a process outside any view was killed: %v", err)
	}
}
