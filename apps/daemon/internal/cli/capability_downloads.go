package cli

import (
	"context"
	"maps"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Use the paired server address for loopback PG downloads: inside Compose,
// the public loopback address points to the runtime container itself.
func withCapabilityDownloads(factory agent.Factory, serverURL string) agent.Factory {
	base, err := url.Parse(serverURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return factory
	}
	return func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		req = capabilityDownloadRequest(req, base)
		return factory(ctx, req, out)
	}
}

func capabilityDownloadURL(raw string, base *url.URL) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return raw
	}
	host := u.Hostname()
	if !strings.EqualFold(host, "localhost") && !net.ParseIP(host).IsLoopback() {
		return raw
	}
	const blobPath = "/internal/blobs/pg:"
	start := strings.Index(u.Path, blobPath)
	if start < 0 {
		return raw
	}
	u.Scheme, u.Host, u.User = base.Scheme, base.Host, nil
	u.Path = strings.TrimRight(base.Path, "/") + u.Path[start:]
	u.RawPath = ""
	return u.String()
}

func capabilityDownloadRequest(req proto.PromptRequestPayload, base *url.URL) proto.PromptRequestPayload {
	opts := maps.Clone(req.AgentOptions)
	for _, key := range []string{"skills", "plugins"} {
		items, ok := opts[key].([]any)
		if !ok {
			continue
		}
		items = slices.Clone(items)
		for i, item := range items {
			descriptor, ok := item.(map[string]any)
			if !ok {
				continue
			}
			raw, _ := descriptor["download_url"].(string)
			if resolved := capabilityDownloadURL(raw, base); resolved != raw {
				updated := maps.Clone(descriptor)
				updated["download_url"] = resolved
				items[i] = updated
			}
		}
		opts[key] = items
	}
	req.AgentOptions = opts
	return req
}

// Executor setup applies the same immutable capability snapshot as ordinary execution.
func withExecutorCapabilities(factory agent.ExecutorFactory, serverURL string) agent.ExecutorFactory {
	base, err := url.Parse(serverURL)
	valid := err == nil && base.Host != "" && (base.Scheme == "http" || base.Scheme == "https")
	return func(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
		if valid {
			req = capabilityDownloadRequest(req, base)
		}
		req = skillUploadRequest(req, serverURL)
		return factory(ctx, req)
	}
}
