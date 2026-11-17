package runtimeobs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// scriptedExporter returns each scripted outcome in turn: nil, an error, or
// "panic".
type scriptedExporter struct {
	mu       sync.Mutex
	outcomes []any
}

func (e *scriptedExporter) Export(context.Context, ExportRecord) error {
	e.mu.Lock()
	outcome := e.outcomes[0]
	e.outcomes = e.outcomes[1:]
	e.mu.Unlock()
	if outcome == "panic" {
		panic("collector client bug")
	}
	err, _ := outcome.(error)
	return err
}

// syncBuffer lets the dispatcher goroutine write while the test reads after Close.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestExportFailureIsReportedOncePerOutage(t *testing.T) {
	logs := &syncBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	defer slog.SetDefault(previous)

	secret := "collector said: token=leaked"
	exporter := &scriptedExporter{outcomes: []any{
		errors.New(secret), errors.New(secret), nil, // one outage, then recovery
		"panic", nil, // a panicking exporter is a second outage
		nil,
	}}
	dispatcher := newExportDispatcher(exporter, ExportOptions{QueueCapacity: 8})
	for range 6 {
		dispatcher.enqueue(ExportRecord{})
	}
	if err := dispatcher.close(t.Context()); err != nil {
		t.Fatal(err)
	}
	output := logs.String()
	if got := strings.Count(output, "Runtime history export failing"); got != 2 {
		t.Fatalf("want one warning per outage (2), got %d:\n%s", got, output)
	}
	if strings.Contains(output, "leaked") {
		t.Fatalf("exporter error text reached the log:\n%s", output)
	}
}
