package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory/postgresreader"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs/otlpexporter"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"golang.org/x/net/http/httpguts"
)

const (
	defaultRuntimeHistoryQueueCapacity     = 256
	defaultRuntimeHistoryTimeoutSeconds    = 2
	maxRuntimeHistoryQueueCapacity         = 4096
	maxRuntimeHistoryTimeoutSeconds        = 30
	minRuntimeHistorySampleIntervalSeconds = 5
	maxRuntimeHistorySampleIntervalSeconds = 300
)

type runtimeHistoryConfig struct {
	Transport             string            `json:"transport,omitempty"`
	Endpoint              string            `json:"endpoint,omitempty"`
	Insecure              bool              `json:"insecure,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	QueueCapacity         int               `json:"queue_capacity,omitempty"`
	TimeoutSeconds        int               `json:"timeout_seconds,omitempty"`
	SampleIntervalSeconds int               `json:"sample_interval_seconds,omitempty"`
}

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

func runtimeHistory(ctx context.Context, coreStore *store.Store, executionEnabled bool) (runtimeHistorySetup, error) {
	config, err := loadRuntimeHistoryConfig()
	if err != nil {
		return runtimeHistorySetup{}, err
	}
	interval := time.Duration(config.SampleIntervalSeconds) * time.Second
	mode := runtimehistory.CollectionPeriodic
	if !executionEnabled {
		interval = 0
		mode = runtimehistory.CollectionOnRead
	}
	capabilities := runtimehistory.Capabilities{
		CollectionMode: mode, SampleInterval: interval, Retention: 7 * 24 * time.Hour,
		MinimumStep: max(30*time.Second, interval), MaximumRange: 24 * time.Hour,
		MaximumPoints: 1000, MaximumSeries: 64, MaximumTotalPoints: 10000,
		Metrics: []runtimehistory.Metric{runtimehistory.MetricCPU, runtimehistory.MetricMemory, runtimehistory.MetricTokens},
	}
	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	backend, err := postgresreader.New(coreStore, postgresreader.Config{Capabilities: capabilities, QueryTimeout: timeout})
	if err != nil {
		return runtimeHistorySetup{}, err
	}
	options := runtimeobs.ExportOptions{QueueCapacity: config.QueueCapacity, Timeout: timeout}
	setup := runtimeHistorySetup{Options: []runtimeobs.ServiceOption{runtimeobs.WithExporter(backend, options)}, Reader: backend, SampleInterval: interval, Prune: backend.Prune}
	if config.Endpoint != "" {
		exporter, err := otlpexporter.New(ctx, otlpexporter.Config{Endpoint: config.Endpoint, Headers: config.Headers, Insecure: config.Insecure, RequestTimeout: timeout})
		if err != nil {
			return runtimeHistorySetup{}, err
		}
		setup.Exporter = exporter
		// Independent queues keep an external Collector outage from delaying local history.
		setup.Options = append(setup.Options, runtimeobs.WithExporter(exporter, options))
	}
	return setup, nil
}

func loadRuntimeHistoryConfig() (runtimeHistoryConfig, error) {
	var config runtimeHistoryConfig
	if file := os.Getenv("OAC_HISTORY_SETTINGS_FILE"); file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return config, errors.New("cannot read OAC_HISTORY_SETTINGS_FILE")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
			return config, errors.New("invalid Runtime history configuration")
		}
	}
	if err := validateRuntimeHistoryConfig(config); err != nil {
		return config, err
	}
	if config.QueueCapacity == 0 {
		config.QueueCapacity = defaultRuntimeHistoryQueueCapacity
	}
	if config.TimeoutSeconds == 0 {
		config.TimeoutSeconds = defaultRuntimeHistoryTimeoutSeconds
	}
	if config.SampleIntervalSeconds == 0 {
		config.SampleIntervalSeconds = 30
	}
	return config, nil
}

func closeRuntimeHistory(ctx context.Context, exporter runtimeHistoryExporter) {
	defer func() { _ = recover() }()
	_ = exporter.Close(ctx)
}

// Retention also runs without active Runtimes. Each bounded pass has its own deadline.
func runHistoryCleanup(ctx context.Context, prune func(context.Context) (int64, error), metrics *coremetrics.Service) {
	if metrics != nil {
		defer metrics.StopJob("history_cleanup")
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		pruneCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		count, err := prune(pruneCtx)
		cancel()
		reportCleanupResult(metrics, "history_cleanup", count, err)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func validateRuntimeHistoryConfig(config runtimeHistoryConfig) error {
	if config.QueueCapacity < 0 || config.QueueCapacity > maxRuntimeHistoryQueueCapacity {
		return errors.New("Runtime history queue_capacity is out of range")
	}
	if config.TimeoutSeconds < 0 || config.TimeoutSeconds > maxRuntimeHistoryTimeoutSeconds {
		return errors.New("Runtime history timeout_seconds is out of range")
	}
	if config.SampleIntervalSeconds != 0 && (config.SampleIntervalSeconds < minRuntimeHistorySampleIntervalSeconds || config.SampleIntervalSeconds > maxRuntimeHistorySampleIntervalSeconds) {
		return errors.New("Runtime history sample_interval_seconds is out of range")
	}
	if config.Endpoint == "" {
		if config.Transport != "" || config.Insecure || len(config.Headers) != 0 {
			return errors.New("Runtime history export options require an endpoint")
		}
		return nil
	}
	if config.Transport != "otlp_http" {
		return errors.New("Runtime history transport must be otlp_http")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.RawPath != "" || endpoint.Path == "" || endpoint.String() != config.Endpoint {
		return errors.New("Runtime history endpoint must be a canonical absolute OTLP metrics URL")
	}
	switch endpoint.Scheme {
	case "https":
		if config.Insecure {
			return errors.New("Runtime history insecure transport requires an http endpoint")
		}
	case "http":
		if !config.Insecure {
			return errors.New("Runtime history http endpoint requires insecure=true")
		}
	default:
		return errors.New("Runtime history endpoint scheme must be https or explicit insecure http")
	}
	for key, value := range config.Headers {
		lower := strings.ToLower(key)
		if !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(value) || lower == "host" || lower == "content-length" || lower == "content-type" || lower == "content-encoding" {
			return errors.New("Runtime history headers contain an invalid or reserved entry")
		}
	}
	return nil
}
