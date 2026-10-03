package log

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"syscall"
	"time"
)

// StartStage reports progress without logging operation inputs or error text.
// Call the returned function once with the stage's result.
func StartStage(stage string, fields ...any) func(error) {
	logger := With(fields...).With("stage", stage)
	started := time.Now()
	logger.Info("Stage started")
	return func(err error) {
		elapsed := time.Since(started).Milliseconds()
		if err != nil {
			logger.Error("Stage failed", append([]any{"elapsed_ms", elapsed}, ErrorFields(err)...)...)
		} else {
			logger.Info("Stage completed", "elapsed_ms", elapsed)
		}
	}
}

// ErrorFields preserves typed diagnostic facts, never arbitrary error messages.
func ErrorFields(err error) []any {
	kind := "unclassified"
	switch {
	case errors.Is(err, context.Canceled):
		kind = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		kind = "timeout"
	case errors.Is(err, fs.ErrNotExist):
		kind = "not_found"
	case errors.Is(err, fs.ErrPermission):
		kind = "permission_denied"
	case errors.Is(err, syscall.ECONNREFUSED):
		kind = "connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		kind = "connection_reset"
	case errors.Is(err, io.ErrUnexpectedEOF):
		kind = "unexpected_eof"
	case errors.Is(err, io.EOF):
		kind = "eof"
	}
	var timeout net.Error
	if kind == "unclassified" && errors.As(err, &timeout) && timeout.Timeout() {
		kind = "timeout"
	}
	fields := []any{"error_kind", kind}
	var path *os.PathError
	if errors.As(err, &path) {
		fields = append(fields, "operation", path.Op, "path", path.Path)
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		fields = append(fields, "errno", int(errno))
	}
	var sql interface{ SQLState() string }
	if errors.As(err, &sql) {
		state := sql.SQLState()
		valid := len(state) == 5
		for _, c := range state {
			valid = valid && (c >= '0' && c <= '9' || c >= 'A' && c <= 'Z')
		}
		if valid {
			fields[1] = "database_error"
			fields = append(fields, "sqlstate", state)
		}
	}
	return fields
}
