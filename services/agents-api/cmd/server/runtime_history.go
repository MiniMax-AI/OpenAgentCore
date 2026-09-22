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

	"golang.org/x/net/http/httpguts"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs/otlpexporter"
)

const (
	defaultRuntimeHistoryQueueCapacity  = 256
	defaultRuntimeHistoryTimeoutSeconds = 2
	maxRuntimeHistoryQueueCapacity      = 4096
	maxRuntimeHistoryTimeoutSeconds     = 30
)

type runtimeHistoryConfig struct {
	Transport      string            `json:"transport"`
	Endpoint       string            `json:"endpoint"`
	Insecure       bool              `json:"insecure"`
	Headers        map[string]string `json:"headers,omitempty"`
	QueueCapacity  int               `json:"queue_capacity,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

type runtimeHistoryExporter interface {
	runtimeobs.Exporter
	Close(context.Context) error
}

func runtimeHistory(ctx context.Context) (runtimeobs.ServiceOption, runtimeHistoryExporter, error) {
	file := os.Getenv("AGENTS_API_RUNTIME_HISTORY_FILE")
	if file == "" {
		return nil, nil, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, errors.New("cannot read AGENTS_API_RUNTIME_HISTORY_FILE")
	}
	var config runtimeHistoryConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, nil, errors.New("invalid Runtime history configuration")
	}
	if err := validateRuntimeHistoryConfig(config); err != nil {
		return nil, nil, err
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
		return nil, nil, err
	}
	return runtimeobs.WithExporter(exporter, runtimeobs.ExportOptions{QueueCapacity: config.QueueCapacity, Timeout: timeout}), exporter, nil
}

func closeRuntimeHistory(ctx context.Context, exporter runtimeHistoryExporter) {
	defer func() {
		_ = recover()
	}()
	_ = exporter.Close(ctx)
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
	for key, value := range config.Headers {
		lower := strings.ToLower(key)
		if !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(value) || lower == "host" || lower == "content-length" || lower == "content-type" || lower == "content-encoding" {
			return errors.New("Runtime history headers contain an invalid or reserved entry")
		}
	}
	return nil
}
