package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func captureStartupLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}

func startupStages(t *testing.T, output *bytes.Buffer) map[string]map[string]any {
	t.Helper()
	stages := map[string]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if stage, ok := row["stage"].(string); ok {
			if _, exists := stages[stage]; exists {
				t.Fatalf("duplicate stage %s", stage)
			}
			stages[stage] = row
		}
	}
	return stages
}

func TestVersionObservationWaitsForExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixtures")
	}
	for _, test := range []struct {
		name, script          string
		wantOutput, wantError bool
	}{
		{"success", "printf 'codex-cli 0.153.4\\n'; sleep 0.04", true, false},
		{"late_failure", "printf 'codex-cli 0.153.4\\n'; sleep 0.04; echo private-secret >&2; exit 7", true, true},
		{"empty", "echo private-secret >&2", false, true},
		{"cancel_after_output", "printf 'codex-cli 0.153.4\\n'; exec sleep 10", true, true},
		{"spawn_failure", "", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := captureStartupLogs(t)
			binary := filepath.Join(t.TempDir(), "probe")
			marker := filepath.Join(t.TempDir(), "output-written")
			content := "#!/bin/sh\n" + test.script + "\n"
			if test.name == "cancel_after_output" {
				content = "#!/bin/sh\nprintf 'codex-cli 0.153.4\\n'; touch '" + marker + "'; exec sleep 10\n"
			}
			if test.name == "spawn_failure" {
				content = "#!/missing-interpreter\n"
			}
			if err := os.WriteFile(binary, []byte(content), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cancellationDone := make(chan struct{})
			if test.name == "cancel_after_output" {
				go func() {
					defer close(cancellationDone)
					for ctx.Err() == nil {
						if _, err := os.Stat(marker); err == nil {
							time.Sleep(100 * time.Millisecond)
							cancel()
							return
						}
						time.Sleep(5 * time.Millisecond)
					}
				}()
			} else {
				close(cancellationDone)
			}
			version, err := CheckCLIAvailable(ctx, binary)
			cancel()
			<-cancellationDone
			if (err != nil) != test.wantError {
				t.Fatalf("version=%q err=%v", version, err)
			}
			if err != nil && version != "" {
				t.Fatal("accepted output before failed exit")
			}
			stages := startupStages(t, output)
			if _, present := stages["first_stdout"]; present != test.wantOutput {
				t.Fatal(stages)
			}
			if test.wantOutput {
				if stages["stdout_to_completion"]["duration_ms"].(float64) < 20 {
					t.Fatal("returned before process completion", stages)
				}
			}
			if strings.Contains(output.String(), "private-secret") || strings.Contains(output.String(), binary) {
				t.Fatal("probe contents leaked")
			}
		})
	}
}

func TestFirstOutputIgnoresEmptyWrites(t *testing.T) {
	var b firstOutputBuffer
	_, _ = b.Write(nil)
	if !b.first.IsZero() {
		t.Fatal("empty output acquired a timestamp")
	}
	_, _ = b.Write([]byte("one"))
	first := b.first
	_, _ = b.Write([]byte("two"))
	if first.IsZero() || b.first != first || b.String() != "onetwo" {
		t.Fatal("first output or bytes changed")
	}
}

func TestCatalogObservationsPreserveFailureBoundaries(t *testing.T) {
	if !SupportsTextVerbosity {
		t.Skip("catalog probe requires Unix")
	}
	for _, test := range []struct {
		name, script, failedStage string
	}{
		{"success", `printf '%s' '{"models":[{"slug":"known","support_verbosity":true}]}'`, ""},
		{"nonzero", `printf '%s' '{"models":[]}'; exit 7`, "model_catalog_command"},
		{"invalid", `printf '%s' '{"models":'`, "model_catalog_validation"},
		{"unsupported", `printf '%s' '{"models":[]}'`, "model_catalog_validation"},
		{"snapshot_failure", `printf '%s' '{"models":[{"slug":"known","support_verbosity":true}]}'`, "model_catalog_snapshot"},
		{"spawn_failure", "", "model_catalog_command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := captureStartupLogs(t)
			home := t.TempDir()
			if test.name == "snapshot_failure" {
				home = filepath.Join(home, "missing")
			}
			plan := SessionPlan{Model: "known", Cwd: t.TempDir(), Env: []string{"CODEX_HOME=" + home}, ExtraConfig: [][2]string{{"model_verbosity", `"high"`}}, Cleanup: func() {}}
			defer func() { plan.Cleanup() }()
			binary := filepath.Join(t.TempDir(), "probe")
			content := "#!/bin/sh\n" + test.script + "\n"
			if test.name == "spawn_failure" {
				content = "#!/missing-interpreter\n"
			}
			if err := os.WriteFile(binary, []byte(content), 0700); err != nil {
				t.Fatal(err)
			}
			err := prepareModelVerbosity(t.Context(), binary, &plan)
			if (err != nil) != (test.failedStage != "") {
				t.Fatal(err)
			}
			stages := startupStages(t, output)
			if stages["model_catalog"]["success"] != (err == nil) {
				t.Fatal(stages)
			}
			if test.failedStage != "" && stages[test.failedStage]["success"] != false {
				t.Fatal(stages)
			}
			if test.failedStage == "model_catalog_command" && stages["model_catalog_validation"] != nil {
				t.Fatal("validated failed command")
			}
			if test.name == "spawn_failure" && stages["model_catalog_cleanup"] != nil {
				t.Fatal("unstarted process reported cleanup")
			}
			if test.name == "success" && stages["model_catalog_cleanup"]["success"] != false {
				t.Fatal("already-exited group signal did not report its error", stages)
			}
			if test.failedStage == "model_catalog_validation" && stages["model_catalog_snapshot"] != nil {
				t.Fatal("persisted invalid catalog")
			}
			if strings.Contains(output.String(), "models") || strings.Contains(output.String(), home) {
				t.Fatal("catalog or path leaked")
			}
		})
	}
}

func TestInitializeObservationDoesNotBlockResponseDelivery(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(writer, nil)))
	defer slog.SetDefault(previous)
	c := NewJSONRPCClient(JSONRPCConfig{})
	c.alive = true
	frames := make(chan JsonRpcRequest, 1)
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	go func() {
		_, err := c.request(ctx, "initialize", nil, func(frame any) error { frames <- frame.(JsonRpcRequest); return nil })
		result <- err
	}()
	var frame JsonRpcRequest
	select {
	case frame = <-frames:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	id, _ := json.Marshal(frame.ID)
	delivered := make(chan struct{})
	go func() { c.handleResponse(id, json.RawMessage(`{}`), nil); close(delivered) }()
	select {
	case <-delivered:
	case <-ctx.Done():
		t.Fatal("response reader blocked on log sink")
	}
	_ = reader.Close()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestInitializeObservationRequiresMatchingResponse(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rejected"}[rejected], func(t *testing.T) {
			output := captureStartupLogs(t)
			c := NewJSONRPCClient(JSONRPCConfig{Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
			c.alive = true
			_, err := c.request(t.Context(), "initialize", nil, func(frame any) error {
				c.handleResponse(json.RawMessage(`"unmatched"`), json.RawMessage(`{}`), nil)
				if strings.Contains(output.String(), "rpc_initialize_response") {
					t.Fatal("orphan acquired timing")
				}
				id, _ := json.Marshal(frame.(JsonRpcRequest).ID)
				var nativeError json.RawMessage
				if rejected {
					nativeError = json.RawMessage(`{"code":1,"message":"private-native-error"}`)
				}
				c.handleResponse(id, json.RawMessage(`{}`), nativeError)
				return nil
			})
			if (err != nil) != rejected {
				t.Fatal(err)
			}
			stages := startupStages(t, output)
			if len(stages) != 2 || stages["rpc_initialize_response"]["success"] != !rejected || stages["rpc_initialize_write"]["success"] != true {
				t.Fatal(stages)
			}
			if strings.Contains(output.String(), "private-native-error") {
				t.Fatal("native response leaked")
			}
		})
	}
}
