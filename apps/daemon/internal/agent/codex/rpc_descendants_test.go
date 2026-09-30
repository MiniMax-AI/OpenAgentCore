package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	stdstrconv "strconv"
	"testing"
	"time"
)

func TestJSONRPCClientOwnsToolDescendants(t *testing.T) {
	for _, mode := range []string{"close", "owner-cancel", "leader-exit", "initialize-error"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := NewJSONRPCClient(JSONRPCConfig{Binary: os.Args[0], ExtraArgs: []string{"-test.run=^TestJSONRPCDescendantHelper$", "--"}, Env: append(os.Environ(), "OAC_RPC_DESCENDANT=leader", "OAC_RPC_DESCENDANT_DIR="+dir, "OAC_RPC_DESCENDANT_MODE="+mode, "GORACE=atexit_sleep_ms=0")})
			t.Cleanup(func() { _ = client.Close() })
			_, err := client.Start(ctx, InitializeParams{})
			if mode == "initialize-error" {
				if err == nil {
					t.Fatal("initialization error accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "child.pid"))
			if err != nil {
				t.Fatal(err)
			}
			pid, err := stdstrconv.Atoi(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			child, err := os.FindProcess(pid)
			if err == nil {
				t.Cleanup(func() {
					if t.Failed() {
						_ = child.Kill()
					}
					_ = child.Release()
				})
			}
			switch mode {
			case "close":
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			case "owner-cancel":
				cancel()
			case "leader-exit":
				if err := client.Notify("exit", nil); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-client.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("native ownership did not settle")
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(dir, "effects"))
			if err != nil || len(before) == 0 {
				t.Fatal("descendant did not begin work", err)
			}
			time.Sleep(100 * time.Millisecond)
			after, err := os.ReadFile(filepath.Join(dir, "effects"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("tool descendant continued after native cleanup", err)
			}
		})
	}
}

func TestJSONRPCDescendantHelper(t *testing.T) {
	role := os.Getenv("OAC_RPC_DESCENDANT")
	if role == "" {
		return
	}
	dir := os.Getenv("OAC_RPC_DESCENDANT_DIR")
	if role == "child" {
		f, err := os.OpenFile(filepath.Join(dir, "effects"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			os.Exit(2)
		}
		defer f.Close()
		for i := 0; i < 3000; i++ {
			if _, err := f.WriteString("effect\n"); err != nil {
				os.Exit(3)
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestJSONRPCDescendantHelper$", "--")
	child.Env = append(os.Environ(), "OAC_RPC_DESCENDANT=child")
	// Retain the RPC pipes as real native tool children can do.
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if child.Start() != nil {
		os.Exit(4)
	}
	if os.WriteFile(filepath.Join(dir, "child.pid"), []byte(stdstrconv.Itoa(child.Process.Pid)), 0600) != nil {
		os.Exit(5)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if info, err := os.Stat(filepath.Join(dir, "effects")); err == nil && info.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(6)
		}
		time.Sleep(time.Millisecond)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(7)
		}
		if request.Method == "exit" {
			os.Exit(0)
		}
		if request.ID != "" {
			response := map[string]any{"id": request.ID, "result": map[string]any{}}
			if os.Getenv("OAC_RPC_DESCENDANT_MODE") == "initialize-error" {
				delete(response, "result")
				response["error"] = map[string]any{"code": -32603, "message": "fixture initialization failure"}
			}
			body, _ := json.Marshal(response)
			fmt.Println(string(body))
		}
	}
	// Leave the leader alive after stdin closes to exercise explicit cancellation.
	time.Sleep(30 * time.Second)
	os.Exit(0)
}
