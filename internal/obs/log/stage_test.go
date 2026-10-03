package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"testing"
)

type databaseFailure struct{ state string }

func (e databaseFailure) Error() string    { return "private database credentials" }
func (e databaseFailure) SQLState() string { return e.state }

func TestStageFailureReportsTypedFactsWithoutErrorText(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind string
	}{
		{"permission", &os.PathError{Op: "open", Path: "/run/oac/installation.id", Err: syscall.EACCES}, "permission_denied"},
		{"missing", fmt.Errorf("private credentials: %w", os.ErrNotExist), "not_found"},
		{"refused", fmt.Errorf("private credentials: %w", syscall.ECONNREFUSED), "connection_refused"},
		{"truncated", io.ErrUnexpectedEOF, "unexpected_eof"},
		{"timeout", context.DeadlineExceeded, "timeout"},
		{"database", databaseFailure{"40P01"}, "database_error"},
		{"unknown", errors.New("private credentials"), "unclassified"},
		{"invalid SQL state", databaseFailure{"private credentials"}, "unclassified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(buildLogger(Config{Out: &output, Format: "json"}))
			defer slog.SetDefault(previous)
			finish := StartStage("database_connection", "component", "core")
			finish(tc.err)
			if strings.Contains(output.String(), "private") {
				t.Fatalf("error text leaked: %s", output.String())
			}
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if len(lines) != 2 {
				t.Fatalf("logs = %s", output.String())
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(lines[1]), &event); err != nil {
				t.Fatal(err)
			}
			if event["msg"] != "Stage failed" || event["stage"] != "database_connection" || event["error_kind"] != tc.kind || event["elapsed_ms"] == nil {
				t.Fatalf("event = %v", event)
			}
			if tc.name == "permission" && (event["path"] != "/run/oac/installation.id" || event["operation"] != "open") {
				t.Fatalf("path facts = %v", event)
			}
			if tc.name == "database" && event["sqlstate"] != "40P01" {
				t.Fatalf("SQL state = %v", event)
			}
		})
	}
}
