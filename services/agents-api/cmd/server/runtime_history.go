package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory/clickhousereader"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs/otlpexporter"
)

const (
	defaultRuntimeHistoryQueueCapacity       = 256
	defaultRuntimeHistoryTimeoutSeconds      = 2
	maxRuntimeHistoryQueueCapacity           = 4096
	maxRuntimeHistoryTimeoutSeconds          = 30
	minRuntimeHistorySampleIntervalSeconds   = 5
	maxRuntimeHistorySampleIntervalSeconds   = 300
	defaultRuntimeHistoryDialTimeoutSeconds  = 5
	defaultRuntimeHistoryQueryTimeoutSeconds = 10
)

type runtimeHistoryConfig struct {
	Transport             string                          `json:"transport"`
	Endpoint              string                          `json:"endpoint"`
	Insecure              bool                            `json:"insecure"`
	Headers               map[string]string               `json:"headers,omitempty"`
	QueueCapacity         int                             `json:"queue_capacity,omitempty"`
	TimeoutSeconds        int                             `json:"timeout_seconds,omitempty"`
	SampleIntervalSeconds int                             `json:"sample_interval_seconds,omitempty"`
	ClickHouse            *runtimeHistoryClickHouseConfig `json:"clickhouse,omitempty"`
}

type runtimeHistoryClickHouseConfig struct {
	Address             string `json:"address"`
	Database            string `json:"database"`
	Username            string `json:"username"`
	Password            string `json:"password,omitempty"`
	Secure              bool   `json:"secure,omitempty"`
	Insecure            bool   `json:"insecure,omitempty"`
	DialTimeoutSeconds  int    `json:"dial_timeout_seconds,omitempty"`
	QueryTimeoutSeconds int    `json:"query_timeout_seconds,omitempty"`
}

type runtimeHistorySetup struct {
	Option         runtimeobs.ServiceOption
	Exporter       runtimeHistoryExporter
	Reader         runtimeHistoryReader
	SampleInterval time.Duration
}

type runtimeHistoryExporter interface {
	runtimeobs.Exporter
	Close(context.Context) error
}

type runtimeHistoryReader interface {
	runtimehistory.Reader
	Close() error
}

var openRuntimeHistoryReader = func(ctx context.Context, config clickhousereader.Config) (runtimeHistoryReader, error) {
	return clickhousereader.Open(ctx, config)
}

func runtimeHistory(ctx context.Context) (runtimeHistorySetup, error) {
	file := os.Getenv("AGENTS_API_RUNTIME_HISTORY_FILE")
	if file == "" {
		return runtimeHistorySetup{}, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return runtimeHistorySetup{}, errors.New("cannot read AGENTS_API_RUNTIME_HISTORY_FILE")
	}
	var config runtimeHistoryConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return runtimeHistorySetup{}, errors.New("invalid Runtime history configuration")
	}
	if err := validateRuntimeHistoryConfig(config); err != nil {
		return runtimeHistorySetup{}, err
	}
	if config.QueueCapacity == 0 {
		config.QueueCapacity = defaultRuntimeHistoryQueueCapacity
	}
	if config.TimeoutSeconds == 0 {
		config.TimeoutSeconds = defaultRuntimeHistoryTimeoutSeconds
	}
	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	exporter, err := otlpexporter.New(ctx, otlpexporter.Config{
		Endpoint: config.Endpoint, Headers: config.Headers, Insecure: config.Insecure, RequestTimeout: timeout,
	})
	if err != nil {
		return runtimeHistorySetup{}, err
	}
	var reader runtimeHistoryReader
	if config.ClickHouse != nil {
		clickhouse := *config.ClickHouse
		if clickhouse.DialTimeoutSeconds == 0 {
			clickhouse.DialTimeoutSeconds = defaultRuntimeHistoryDialTimeoutSeconds
		}
		if clickhouse.QueryTimeoutSeconds == 0 {
			clickhouse.QueryTimeoutSeconds = defaultRuntimeHistoryQueryTimeoutSeconds
		}
		mode := runtimehistory.CollectionOnRead
		interval := time.Duration(config.SampleIntervalSeconds) * time.Second
		if interval > 0 {
			mode = runtimehistory.CollectionPeriodic
		}
		minimumStep := 30 * time.Second
		if interval > minimumStep {
			minimumStep = interval
		}
		reader, err = openRuntimeHistoryReader(ctx, clickhousereader.Config{
			Address: clickhouse.Address, Database: clickhouse.Database, Username: clickhouse.Username, Password: clickhouse.Password, Secure: clickhouse.Secure, Insecure: clickhouse.Insecure,
			DialTimeout: time.Duration(clickhouse.DialTimeoutSeconds) * time.Second, QueryTimeout: time.Duration(clickhouse.QueryTimeoutSeconds) * time.Second,
			Capabilities: runtimehistory.Capabilities{
				CollectionMode: mode, SampleInterval: interval, Retention: 7 * 24 * time.Hour, MinimumStep: minimumStep, MaximumRange: 24 * time.Hour,
				MaximumPoints: 1_000, MaximumSeries: 64, MaximumTotalPoints: 10_000,
				Metrics: []runtimehistory.Metric{runtimehistory.MetricCPU, runtimehistory.MetricMemory, runtimehistory.MetricTokens},
			},
		})
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closeRuntimeHistory(closeCtx, exporter)
			return runtimeHistorySetup{}, err
		}
	}
	return runtimeHistorySetup{
		Option:         runtimeobs.WithExporter(exporter, runtimeobs.ExportOptions{QueueCapacity: config.QueueCapacity, Timeout: timeout}),
		Exporter:       exporter,
		Reader:         reader,
		SampleInterval: time.Duration(config.SampleIntervalSeconds) * time.Second,
	}, nil
}

func closeRuntimeHistory(ctx context.Context, exporter runtimeHistoryExporter) {
	defer func() {
		_ = recover()
	}()
	_ = exporter.Close(ctx)
}

func closeRuntimeHistoryReader(reader runtimeHistoryReader) {
	defer func() {
		_ = recover()
	}()
	_ = reader.Close()
}

func validateRuntimeHistoryConfig(config runtimeHistoryConfig) error {
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
	if config.QueueCapacity < 0 || config.QueueCapacity > maxRuntimeHistoryQueueCapacity {
		return errors.New("Runtime history queue_capacity is out of range")
	}
	if config.TimeoutSeconds < 0 || config.TimeoutSeconds > maxRuntimeHistoryTimeoutSeconds {
		return errors.New("Runtime history timeout_seconds is out of range")
	}
	if config.SampleIntervalSeconds != 0 && (config.SampleIntervalSeconds < minRuntimeHistorySampleIntervalSeconds || config.SampleIntervalSeconds > maxRuntimeHistorySampleIntervalSeconds) {
		return errors.New("Runtime history sample_interval_seconds is out of range")
	}
	if config.ClickHouse != nil {
		clickhouse := config.ClickHouse
		host, _, addressErr := net.SplitHostPort(clickhouse.Address)
		if addressErr != nil || host == "" || clickhouse.Database == "" || clickhouse.Username == "" || clickhouse.Secure == clickhouse.Insecure || len(clickhouse.Database) > 128 || len(clickhouse.Username) > 128 || strings.ContainsAny(clickhouse.Database+clickhouse.Username, "\x00\r\n") {
			return errors.New("Runtime history ClickHouse configuration is invalid")
		}
		if clickhouse.DialTimeoutSeconds < 0 || clickhouse.DialTimeoutSeconds > maxRuntimeHistoryTimeoutSeconds || clickhouse.QueryTimeoutSeconds < 0 || clickhouse.QueryTimeoutSeconds > maxRuntimeHistoryTimeoutSeconds {
			return errors.New("Runtime history ClickHouse timeout is out of range")
		}
	}
	for key, value := range config.Headers {
		lower := strings.ToLower(key)
		if !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(value) || lower == "host" || lower == "content-length" || lower == "content-type" || lower == "content-encoding" {
			return errors.New("Runtime history headers contain an invalid or reserved entry")
		}
	}
	return nil
}
