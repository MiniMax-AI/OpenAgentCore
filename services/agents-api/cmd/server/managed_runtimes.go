package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	sandboxcubesandbox "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/cubesandbox"
	sandboxdocker "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/docker"
	"github.com/moby/moby/client"
)

// Two backends, one provider namespace. A provider key pins exactly one
// backend, so a retained entry keeps its cleanup backend when the default moves.
type managedRuntimeConfig struct {
	CoreURL         string                         `json:"core_url"`
	EngineProviders map[string]string              `json:"engine_providers"`
	DefaultProvider string                         `json:"default_provider"`
	Docker          map[string]managedDockerConfig `json:"docker"`
	CubeSandbox     map[string]managedCubeConfig   `json:"cubesandbox"`
}

// managedCubeConfig is trusted operator configuration for a remote microVM
// backend. api_key_file is a private file: the key is never an inline config
// value, an environment variable or a log line. host_mount_root is the host
// directory prefix every per-allocation store lives under, and must be one
// CubeMaster allows in allowed_host_mount_prefixes. platform_egress lists the
// addresses a Session with network access disabled must still reach.
type managedCubeConfig struct {
	APIURL         string   `json:"api_url"`
	ProxyNodeIP    string   `json:"proxy_node_ip"`
	SandboxDomain  string   `json:"sandbox_domain"`
	ProxyScheme    string   `json:"proxy_scheme"`
	Template       string   `json:"template"`
	LeaseSeconds   int      `json:"lease_seconds"`
	APIKeyFile     string   `json:"api_key_file"`
	HostMountRoot  string   `json:"host_mount_root"`
	PlatformEgress []string `json:"platform_egress"`
}

type managedDockerConfig struct {
	Host          string   `json:"host"`
	Image         string   `json:"image"`
	Network       string   `json:"network"`
	SeccompFile   string   `json:"seccomp_file"`
	ExtraHosts    []string `json:"extra_hosts"`
	NestedSandbox bool     `json:"nested_sandbox"`
}

// Each provider key pins an explicit backend, independently of ambient
// DOCKER_HOST. Retained entries keep their cleanup backend when the default changes.
func managedRuntimes() (*execution.RuntimeProviders, func(), error) {
	file := os.Getenv("AGENTS_API_MANAGED_RUNTIMES_FILE")
	closeAll := func() {}
	if file == "" {
		return nil, closeAll, nil
	}
	if os.Getenv("AGENTS_API_DAEMON_WS_URL") == "" {
		return nil, closeAll, errors.New("managed Runtime configuration requires AGENTS_API_DAEMON_WS_URL")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, closeAll, errors.New("cannot read AGENTS_API_MANAGED_RUNTIMES_FILE")
	}
	var config managedRuntimeConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || len(config.Docker)+len(config.CubeSandbox) == 0 {
		return nil, closeAll, errors.New("invalid managed Runtime configuration")
	}
	configured := func(key string) bool {
		_, dockerOK := config.Docker[key]
		_, cubeOK := config.CubeSandbox[key]
		return dockerOK || cubeOK
	}
	if config.DefaultProvider != "" && !configured(config.DefaultProvider) {
		return nil, closeAll, errors.New("managed default provider is not configured")
	}
	if config.EngineProviders == nil {
		config.EngineProviders = map[string]string{}
	}
	defaultEngine := os.Getenv("AGENTS_API_ENGINE")
	if defaultEngine == "" {
		defaultEngine = "codex"
	}
	if config.DefaultProvider != "" && config.EngineProviders[defaultEngine] == "" {
		config.EngineProviders[defaultEngine] = config.DefaultProvider
	}
	for kind, key := range config.EngineProviders {
		_, qualified := (engine.Catalog{}).Lookup(kind)
		if !qualified || key == "" || !configured(key) {
			return nil, closeAll, errors.New("invalid managed engine provider mapping")
		}
	}
	// One key, one backend: a duplicate would make cleanup ambiguous.
	for key := range config.CubeSandbox {
		if _, duplicate := config.Docker[key]; duplicate {
			return nil, closeAll, errors.New("managed provider keys must be unique")
		}
	}
	var clients []*client.Client
	var cubes []*sandboxcubesandbox.Provider
	closeAll = func() {
		for _, c := range clients {
			_ = c.Close()
		}
		for _, p := range cubes {
			p.Close()
		}
	}
	result := &execution.RuntimeProviders{EngineProviders: config.EngineProviders, CoreURL: config.CoreURL, DefaultProvider: config.DefaultProvider, Providers: map[string]sandbox.Provider{}}
	for key, entry := range config.CubeSandbox {
		keyBytes, err := os.ReadFile(entry.APIKeyFile)
		if err != nil {
			closeAll()
			return nil, func() {}, errors.New("cannot read managed CubeSandbox API key file")
		}
		if strings.TrimSpace(string(keyBytes)) == "" {
			closeAll()
			return nil, func() {}, errors.New("managed CubeSandbox API key file is empty")
		}
		provider, err := sandboxcubesandbox.New(sandboxcubesandbox.Config{InstallationID: key, APIURL: entry.APIURL, ProxyNodeIP: entry.ProxyNodeIP, SandboxDomain: entry.SandboxDomain, ProxyScheme: entry.ProxyScheme, Template: entry.Template, APIKey: strings.TrimSpace(string(keyBytes)), LeaseSeconds: entry.LeaseSeconds, HostMountRoot: entry.HostMountRoot, PlatformEgress: entry.PlatformEgress})
		if err != nil {
			closeAll()
			return nil, func() {}, errors.New("invalid managed CubeSandbox provider configuration")
		}
		cubes = append(cubes, provider)
		result.Providers[key] = provider
	}
	for key, entry := range config.Docker {
		// V1 qualifies a local Docker daemon. Remote executor/provider transports are
		// separate work; do not silently inherit a different backend from the shell.
		if !strings.HasPrefix(entry.Host, "unix:///") || len(entry.Host) <= 8 {
			closeAll()
			return nil, func() {}, errors.New("managed Docker host must be an explicit unix socket")
		}
		seccomp, err := os.ReadFile(entry.SeccompFile)
		if err != nil || !json.Valid(seccomp) {
			closeAll()
			return nil, func() {}, errors.New("cannot read managed Docker seccomp JSON")
		}
		c, err := client.New(client.WithHost(entry.Host))
		if err != nil {
			closeAll()
			return nil, func() {}, errors.New("invalid managed Docker endpoint")
		}
		clients = append(clients, c)
		provider, err := sandboxdocker.New(c, sandboxdocker.Config{InstallationID: key, Image: entry.Image, Network: entry.Network, Seccomp: string(seccomp), ExtraHosts: entry.ExtraHosts, NestedSandbox: entry.NestedSandbox})
		if err != nil {
			closeAll()
			return nil, func() {}, errors.New("invalid managed Docker provider configuration")
		}
		result.Providers[key] = provider
	}
	return result, closeAll, nil
}
