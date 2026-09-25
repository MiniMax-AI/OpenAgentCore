package main

import (
	"context"
	"errors"
	"io"
	stdlog "log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
)

type console struct {
	config
	root                      *os.Root
	nodePayload               *os.Root
	nodeInstallerDigest       string
	selfHostedInstallerDigest string
	proxy                     *httputil.ReverseProxy
	transport                 *http.Transport
	host                      string
	auth                      *consoleAuth
}

type consoleActorContextKey struct{}

func newConsole(c config) (*console, error) {
	if c.coreKey == "" {
		return nil, errors.New("console requires the Core key")
	}
	root, err := os.OpenRoot(c.dist)
	if err != nil {
		return nil, errors.New("cannot open console assets")
	}
	index, err := root.Stat("index.html")
	if err != nil || !index.Mode().IsRegular() {
		root.Close()
		return nil, errors.New("console assets require index.html")
	}
	origin, _ := url.Parse(c.origin)
	h := &console{config: c, root: root, host: origin.Host, auth: newConsoleAuth(c)}
	if c.nodePayloadDir != "" {
		h.nodePayload, err = os.OpenRoot(c.nodePayloadDir)
		if err != nil {
			root.Close()
			return nil, errors.New("cannot open node installation payload")
		}
		h.nodeInstallerDigest, err = installerDigest(h.nodePayload, "node-install.pyz")
		if err == nil {
			h.selfHostedInstallerDigest, err = installerDigest(h.nodePayload, "self-hosted-install.pyz")
		}
		if err != nil {
			h.nodePayload.Close()
			root.Close()
			return nil, err
		}
	}
	h.transport = http.DefaultTransport.(*http.Transport).Clone()
	// Credentials go only to the configured Core, never an ambient HTTP proxy.
	h.transport.Proxy = nil
	h.proxy = &httputil.ReverseProxy{
		Transport:     h.transport,
		FlushInterval: -1,
		ErrorLog:      stdlog.New(io.Discard, "", 0),
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(c.upstream)
			r.Out.Header.Del("Authorization")
			r.Out.Header.Del("Proxy-Authorization")
			r.Out.Header.Del("Cookie")
			r.Out.Header.Del("Origin")
			r.Out.Header.Del("Referer")
			r.Out.Header.Set("Authorization", "Bearer "+c.coreKey)
			actor, _ := r.In.Context().Value(consoleActorContextKey{}).(string)
			r.Out.Header.Set("X-Core-Console-Actor", actor)
		},
		ModifyResponse: func(r *http.Response) error {
			// Never send a browser to a different origin with its cached login.
			if r.StatusCode >= 300 && r.StatusCode < 400 {
				return errors.New("Core redirects are not supported")
			}
			r.Header.Del("Set-Cookie")
			r.Header.Del("WWW-Authenticate")
			r.Header.Del("Location")
			r.Header.Del("Refresh")
			for key := range r.Header {
				if strings.HasPrefix(strings.ToLower(key), "access-control-") {
					r.Header.Del(key)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Core is unavailable", http.StatusBadGateway)
		},
	}
	return h, nil
}

func (h *console) Close() {
	h.transport.CloseIdleConnections()
	_ = h.root.Close()
	if h.nodePayload != nil {
		_ = h.nodePayload.Close()
	}
}

func (h *console) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	if r.URL.Path == "/healthz" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	if coreDirectRequest(r) || r.URL.Path == "/console/api-keys" || strings.HasPrefix(r.URL.Path, "/console/api-keys/") {
		http.NotFound(w, r)
		return
	}
	if h.nodePayload != nil && strings.HasPrefix(r.URL.Path, "/node-install/") {
		if r.Host != h.host || !safePath(r.URL.Path) || r.URL.IsAbs() {
			http.NotFound(w, r)
			return
		}
		h.serveNodePayload(w, r)
		return
	}
	if !h.sameOrigin(r) {
		authError(w, http.StatusForbidden, "Forbidden")
		return
	}
	if !safePath(r.URL.Path) || r.URL.IsAbs() || r.Method == http.MethodConnect || r.Method == http.MethodTrace || r.Header.Get("Upgrade") != "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if r.URL.Path == "/console/auth" || strings.HasPrefix(r.URL.Path, "/console/auth/") {
		h.auth.serve(w, r)
		return
	}
	if publicConsoleAsset(r) {
		h.serveStatic(w, r)
		return
	}
	if !h.auth.authenticated(r) {
		authError(w, http.StatusUnauthorized, "Sign in to the console")
		return
	}
	if r.URL.Path == "/console/config" && r.Method == http.MethodGet {
		h.serveConsoleConfiguration(w, r)
		return
	}
	if r.URL.Path == "/core" || strings.HasPrefix(r.URL.Path, "/core/") {
		if !coreRequest(r) {
			http.NotFound(w, r)
			return
		}
		// Core records this fixed audit label for display only; there are no console users.
		h.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), consoleActorContextKey{}, "console")))
		return
	}
	h.serveStatic(w, r)
}

func (h *console) sameOrigin(r *http.Request) bool {
	if r.Host != h.host || !h.validOriginHeaders(r) {
		return false
	}
	// Modern browsers provide Fetch Metadata; older same-origin requests carry
	// Origin or Referer. Reject an ambiguous browser mutation before proxying.
	if r.Method != http.MethodGet && r.Method != http.MethodHead &&
		r.Header.Get("Origin") == "" && r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		referrer, err := url.Parse(r.Referer())
		if err != nil || referrer.Scheme+"://"+referrer.Host != h.origin {
			return false
		}
	}
	return true
}

func (h *console) validOriginHeaders(r *http.Request) bool {
	if origins := r.Header.Values("Origin"); len(origins) > 1 || (len(origins) == 1 && origins[0] != h.origin) {
		return false
	}
	sites := r.Header.Values("Sec-Fetch-Site")
	if len(sites) > 1 {
		return false
	}
	if len(sites) == 1 && sites[0] != "same-origin" && sites[0] != "none" {
		return false
	}
	return true
}

func safePath(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00%") {
		return false
	}
	return path.Clean(value) == strings.TrimSuffix(value, "/") || value == "/"
}

func (h *console) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	file, err := h.root.Open(name)
	if errors.Is(err, os.ErrNotExist) && path.Ext(name) == "" && !strings.HasPrefix(name, "api/") {
		file, err = h.root.Open("index.html")
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}
