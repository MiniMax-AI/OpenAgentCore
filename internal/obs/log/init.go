package log

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
)

// Config drives Init.
type Config struct {
	// Format is "json" or "text". Empty auto-detects: text on a TTY,
	// JSON otherwise.
	Format string
	// Level is the minimum slog level. Empty defaults to Info.
	Level slog.Level
	// AddSource toggles slog's filename:line attribute (~hundreds of
	// ns per line — fine in dev, costly in prod).
	AddSource bool
	// Out is the destination writer. Nil defaults to os.Stderr.
	Out io.Writer
}

// LoadConfig reads the logging settings Core and Web share:
//
//	OAC_LOG_LEVEL      = debug | info | warn | error  (default: info)
//	OAC_LOG_FORMAT     = auto | json | text  (default: auto)
//	OAC_LOG_ADD_SOURCE = 0 | 1  (default: 0)
//
// Unset or empty selects the default. Any other value is an error that names
// the variable and never echoes the value.
func LoadConfig() (Config, error) {
	var cfg Config
	switch os.Getenv("OAC_LOG_LEVEL") {
	case "", "info":
	case "debug":
		cfg.Level = slog.LevelDebug
	case "warn":
		cfg.Level = slog.LevelWarn
	case "error":
		cfg.Level = slog.LevelError
	default:
		return Config{}, errors.New("OAC_LOG_LEVEL must be debug, info, warn or error")
	}
	switch format := os.Getenv("OAC_LOG_FORMAT"); format {
	case "", "auto":
	case "json", "text":
		cfg.Format = format
	default:
		return Config{}, errors.New("OAC_LOG_FORMAT must be auto, json or text")
	}
	switch os.Getenv("OAC_LOG_ADD_SOURCE") {
	case "", "0":
	case "1":
		cfg.AddSource = true
	default:
		return Config{}, errors.New("OAC_LOG_ADD_SOURCE must be 0 or 1")
	}
	return cfg, nil
}

// isTerminal reports whether f is a character device (TTY) so the JSON
// vs. text auto-detect doesn't need the golang.org/x/term dep.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// initOnce guarantees Init's slog.SetDefault side-effect runs at most
// once per process so tests don't fight over the global handler.
var initOnce sync.Once

// Init installs ContextHandler as slog.Default. Calling more than once
// is a no-op — only one global slog handler exists.
func Init(cfg Config) {
	initOnce.Do(func() {
		slog.SetDefault(buildLogger(cfg))
	})
}

// buildLogger is split out so tests can build a logger without
// triggering the global SetDefault side-effect.
func buildLogger(cfg Config) *slog.Logger {
	out := cfg.Out
	if out == nil {
		out = os.Stderr
	}
	opts := &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: cfg.AddSource,
	}
	format := cfg.Format
	if format == "" {
		format = "json"
		if f, ok := out.(*os.File); ok && isTerminal(f) {
			format = "text"
		}
	}
	var inner slog.Handler
	switch format {
	case "text":
		inner = slog.NewTextHandler(out, opts)
	default:
		inner = slog.NewJSONHandler(out, opts)
	}
	return slog.New(NewContextHandler(inner))
}
