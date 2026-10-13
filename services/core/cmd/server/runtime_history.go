package main

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/runtimehistorypg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/processconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs/otlpexporter"
)

type runtimeHistorySetup struct {
	Options        []runtimeobs.ServiceOption
	Exporter       runtimeHistoryExporter
	Reader         runtimehistory.Reader
	SampleInterval time.Duration
	Prune          func(context.Context) (int64, error)
}

type runtimeHistoryExporter interface {
	runtimeobs.Exporter
	Close(context.Context) error
}

func runtimeHistory(ctx context.Context, units *pgunit.Pool, config processconfig.RuntimeHistory) (runtimeHistorySetup, error) {
	capabilities := runtimehistory.Capabilities{
		SampleInterval: config.SampleInterval, Retention: 7 * 24 * time.Hour,
		MinimumStep: max(30*time.Second, config.SampleInterval), MaximumRange: 24 * time.Hour,
		MaximumPoints: 1000, MaximumSeries: 64, MaximumTotalPoints: 10000,
		Metrics: []runtimehistory.Metric{runtimehistory.MetricCPU, runtimehistory.MetricMemory, runtimehistory.MetricTokens},
	}
	backend, err := runtimehistorypg.New(units, runtimehistorypg.Config{Capabilities: capabilities, QueryTimeout: config.Timeout})
	if err != nil {
		return runtimeHistorySetup{}, err
	}
	options := runtimeobs.ExportOptions{QueueCapacity: config.QueueCapacity, Timeout: config.Timeout}
	setup := runtimeHistorySetup{Options: []runtimeobs.ServiceOption{runtimeobs.WithExporter(backend, options)}, Reader: backend, SampleInterval: config.SampleInterval, Prune: backend.Prune}
	if config.Endpoint != "" {
		exporter, err := otlpexporter.New(ctx, otlpexporter.Config{Endpoint: config.Endpoint, Headers: config.Headers, Insecure: config.Insecure, RequestTimeout: config.Timeout})
		if err != nil {
			return runtimeHistorySetup{}, err
		}
		setup.Exporter = exporter
		// Independent queues keep an external Collector outage from delaying local history.
		setup.Options = append(setup.Options, runtimeobs.WithExporter(exporter, options))
	}
	return setup, nil
}

func closeRuntimeHistory(ctx context.Context, exporter runtimeHistoryExporter) {
	defer func() { _ = recover() }()
	_ = exporter.Close(ctx)
}
