package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// OrcaRouter is a named model provider for the console. Its inference base and
// its authentication origin are different public origins and the console never
// derives one from the other. The console server owns both URLs so the browser
// sends only same-origin requests, and it performs catalog discovery so the
// operator's key never leaves the form it was typed into.
const (
	defaultOrcaAuthOrigin = "https://www.orcarouter.ai"
	defaultOrcaAPIOrigin  = "https://api.orcarouter.ai"
	// The catalog is bounded so an unexpected response cannot consume unbounded
	// memory: 512 KiB and 2000 records are far above the live catalog's size.
	orcaCatalogByteLimit = 512 << 10
	orcaCatalogItemLimit = 2000
	orcaCatalogTimeout   = 15 * time.Second
	orcaAuthorizationURL = defaultOrcaAuthOrigin + "/auth"
	orcaKeyConsoleURL    = defaultOrcaAuthOrigin + "/console/authorized-apps"
)

// orcaRouter holds the origins this deployment talks to. `ORCA_BASE_URL` is the
// shared self-hosted fallback; the explicit overrides win over it.
type orcaRouter struct {
	authOrigin string
	apiOrigin  string
	transport  *http.Transport
}

func loadOrcaRouter() (orcaRouter, error) {
	shared := strings.TrimRight(strings.TrimSpace(os.Getenv("ORCA_BASE_URL")), "/")
	auth := strings.TrimRight(strings.TrimSpace(os.Getenv("ORCA_AUTH_BASE_URL")), "/")
	api := strings.TrimRight(strings.TrimSpace(os.Getenv("ORCA_API_BASE_URL")), "/")
	if auth == "" {
		auth = shared
	}
	if api == "" {
		api = shared
	}
	if auth == "" {
		auth = defaultOrcaAuthOrigin
	}
	if api == "" {
		api = defaultOrcaAPIOrigin
	}
	for _, origin := range []string{auth, api} {
		if !allowedOrcaOrigin(origin) {
			return orcaRouter{}, errors.New("OrcaRouter origins require HTTPS, or a loopback HTTP origin")
		}
	}
	// Credentials and catalog requests reach OrcaRouter directly, never an
	// ambient HTTP proxy.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return orcaRouter{authOrigin: auth, apiOrigin: api, transport: transport}, nil
}

// allowedOrcaOrigin admits an HTTPS origin and the loopback HTTP origin a
// self-hosted deployment runs on. Anything else is refused before a request.
func allowedOrcaOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.Path != "" || strings.ContainsAny(origin, "\\\x00\r\n") {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}

// catalogURL joins the configured API origin with the fixed `/v1/models` path.
// A self-hosted value that already ends in `/v1` is not doubled.
func (o orcaRouter) catalogURL(capability string) *url.URL {
	base := strings.TrimRight(o.apiOrigin, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	target, err := url.Parse(base + "/models")
	if err != nil {
		return nil
	}
	if capability != "" {
		target.RawQuery = url.Values{"capability": []string{capability}}.Encode()
	}
	return target
}

// inferenceBase is the address the console writes to Core for this provider: the
// API origin plus its `/v1` segment, so an operator never types one.
func (o orcaRouter) inferenceBase() string {
	base := strings.TrimRight(o.apiOrigin, "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

// exchangeURL is the OrcaRouter code exchange. It lives on the auth origin under
// `/api/v1/auth/keys`; `/v1/auth/keys` on the inference origin is a 404.
func (o orcaRouter) exchangeURL() string {
	return strings.TrimRight(o.authOrigin, "/") + "/api/v1/auth/keys"
}

// catalogModel is the minimal model metadata the browser receives. Pricing,
// descriptions and provider internals stay on the server.
type catalogModel struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	ContextLength      *int64   `json:"context_length,omitempty"`
	MaxCompletion      *int64   `json:"max_completion_tokens,omitempty"`
	InputModalities    []string `json:"input_modalities,omitempty"`
	EndpointTypes      []string `json:"supported_endpoint_types,omitempty"`
	ModalitiesDeclared bool     `json:"modalities_declared"`
}

type orcaCatalogResponse struct {
	Models []catalogModel `json:"models"`
	// Origin is the API origin this deployment reads, so the console can name the
	// catalog it is showing without guessing at an override.
	Origin string `json:"catalog_origin"`
	// Degraded is true when OrcaRouter could not be read and the caller must use
	// its own verified fallback catalog.
	Degraded bool   `json:"degraded"`
	Reason   string `json:"reason,omitempty"`
}

func writeConsoleJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// serveOrcaRouter answers the console's OrcaRouter requests. The caller has
// already signed in and passed the same-origin checks.
func (h *console) serveOrcaRouter(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/console/orcarouter/config" && r.Method == http.MethodGet:
		h.serveOrcaConfiguration(w, r)
	case r.URL.Path == "/console/orcarouter/catalog" && r.Method == http.MethodGet:
		h.serveOrcaCatalog(w, r)
	case r.URL.Path == "/console/orcarouter/exchange" && r.Method == http.MethodPost:
		h.serveOrcaExchange(w, r)
	default:
		http.NotFound(w, r)
	}
}

// serveOrcaConfiguration names the origins this deployment talks to. The browser
// composes the authorize URL and the inference base from them instead of
// hardcoding the public ones, so an override deployment stays correct. Neither
// value is secret and no catalog or credential request happens here.
func (h *console) serveOrcaConfiguration(w http.ResponseWriter, _ *http.Request) {
	authOrigin := strings.TrimRight(h.orca.authOrigin, "/")
	writeConsoleJSON(w, http.StatusOK, map[string]string{
		"object":         "console.orcarouter",
		"auth_origin":    authOrigin,
		"api_origin":     strings.TrimRight(h.orca.apiOrigin, "/"),
		"authorize_url":  authOrigin + "/auth",
		"inference_base": h.orca.inferenceBase(),
		"key_console":    orcaKeyConsoleURL,
	})
}

// serveOrcaCatalog reads the configured catalog with the operator's own key.
// The key arrives in a request header, is never placed in a URL, never logged
// and never returned; only reduced model metadata reaches the browser.
func (h *console) serveOrcaCatalog(w http.ResponseWriter, r *http.Request) {
	capability := r.URL.Query().Get("capability")
	switch capability {
	case "", "chat", "embedding", "image", "video", "rerank":
	default:
		writeConsoleJSON(w, http.StatusBadRequest, map[string]string{"error": "Unknown catalog capability"})
		return
	}
	key := strings.TrimSpace(r.Header.Get("X-OrcaRouter-Key"))
	if key == "" || len(key) > 4096 || strings.ContainsAny(key, "\x00\r\n") {
		writeConsoleJSON(w, http.StatusBadRequest, map[string]string{"error": "An OrcaRouter API key is required to read the model catalog"})
		return
	}
	target := h.orca.catalogURL(capability)
	if target == nil {
		writeConsoleJSON(w, http.StatusBadGateway, orcaCatalogResponse{Degraded: true, Reason: "catalog_request_invalid"})
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		writeConsoleJSON(w, http.StatusBadGateway, orcaCatalogResponse{Degraded: true, Reason: "catalog_request_invalid"})
		return
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: orcaCatalogTimeout, Transport: h.orca.transport, CheckRedirect: refuseRedirect}
	response, err := client.Do(request)
	if err != nil {
		writeConsoleJSON(w, http.StatusBadGateway, orcaCatalogResponse{Degraded: true, Reason: "catalog_unreachable"})
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		writeConsoleJSON(w, http.StatusUnauthorized, map[string]string{"error": "OrcaRouter rejected this API key"})
		return
	}
	if response.StatusCode != http.StatusOK {
		writeConsoleJSON(w, http.StatusBadGateway, orcaCatalogResponse{Degraded: true, Reason: "catalog_status"})
		return
	}
	body, err := readBounded(response.Body, orcaCatalogByteLimit)
	if err != nil {
		writeConsoleJSON(w, http.StatusBadGateway, orcaCatalogResponse{Degraded: true, Reason: "catalog_too_large"})
		return
	}
	var payload struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Data == nil {
		writeConsoleJSON(w, http.StatusBadGateway, orcaCatalogResponse{Degraded: true, Reason: "catalog_shape"})
		return
	}
	models := make([]catalogModel, 0, min(len(payload.Data), orcaCatalogItemLimit))
	for _, raw := range payload.Data {
		if len(models) >= orcaCatalogItemLimit {
			break
		}
		if model, ok := reduceCatalogModel(raw); ok {
			models = append(models, model)
		}
	}
	writeConsoleJSON(w, http.StatusOK, orcaCatalogResponse{Models: models, Origin: strings.TrimRight(h.orca.apiOrigin, "/")})
}

// serveOrcaExchange redeems a PKCE authorization code. The verifier is supplied
// by the requesting browser and never stored; only the issued key is returned,
// once, inside the response body.
func (h *console) serveOrcaExchange(w http.ResponseWriter, r *http.Request) {
	if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		writeConsoleJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Use application/json"})
		return
	}
	var input struct {
		Code                string `json:"code"`
		CodeVerifier        string `json:"code_verifier"`
		CodeChallengeMethod string `json:"code_challenge_method"`
	}
	body, err := readBounded(r.Body, 8192)
	if err != nil || json.Unmarshal(body, &input) != nil {
		writeConsoleJSON(w, http.StatusBadRequest, map[string]string{"error": "Send a JSON object with code, code_verifier and code_challenge_method"})
		return
	}
	// S256 is mandatory: the consent screen can hand the code to a person, and a
	// `plain` challenge would travel on the authorize URL with the verifier.
	if input.CodeChallengeMethod != "S256" || input.Code == "" || input.CodeVerifier == "" ||
		len(input.Code) > 4096 || len(input.CodeVerifier) > 512 ||
		strings.ContainsAny(input.Code+input.CodeVerifier, "\x00\r\n") {
		writeConsoleJSON(w, http.StatusBadRequest, map[string]string{"error": "A one-time code and its S256 verifier are required"})
		return
	}
	payload, err := json.Marshal(map[string]string{
		"code":                  input.Code,
		"code_verifier":         input.CodeVerifier,
		"code_challenge_method": "S256",
	})
	if err != nil {
		writeConsoleJSON(w, http.StatusBadGateway, map[string]string{"error": "OrcaRouter authorization failed"})
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.orca.exchangeURL(), strings.NewReader(string(payload)))
	if err != nil {
		writeConsoleJSON(w, http.StatusBadGateway, map[string]string{"error": "OrcaRouter authorization failed"})
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: orcaCatalogTimeout, Transport: h.orca.transport, CheckRedirect: refuseRedirect}
	response, err := client.Do(request)
	if err != nil {
		writeConsoleJSON(w, http.StatusBadGateway, map[string]string{"error": "OrcaRouter authorization could not be reached; try again"})
		return
	}
	defer response.Body.Close()
	answer, err := readBounded(response.Body, 8192)
	if err != nil {
		writeConsoleJSON(w, http.StatusBadGateway, map[string]string{"error": "OrcaRouter authorization failed"})
		return
	}
	// The exchange reports errors as a plain OAuth envelope, which is never
	// forwarded: an upstream body could carry a credential.
	if response.StatusCode != http.StatusOK {
		writeConsoleJSON(w, http.StatusBadRequest, map[string]string{"error": orcaExchangeMessage(response.StatusCode)})
		return
	}
	var granted struct {
		Key   string `json:"key"`
		Scope string `json:"scope"`
	}
	if json.Unmarshal(answer, &granted) != nil || granted.Key == "" {
		writeConsoleJSON(w, http.StatusBadGateway, map[string]string{"error": "OrcaRouter returned no key; authorize again"})
		return
	}
	// Read the granted scope back: it is what was approved, not what was asked for.
	if granted.Scope != "" && granted.Scope != "api" {
		writeConsoleJSON(w, http.StatusBadRequest, map[string]string{"error": "The approved OrcaRouter scope does not permit this application"})
		return
	}
	writeConsoleJSON(w, http.StatusOK, map[string]string{"key": granted.Key, "scope": "api", "provider": "orcarouter"})
}

func orcaExchangeMessage(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "The OrcaRouter code was rejected; start the connection again"
	case http.StatusForbidden:
		return "The OrcaRouter code is unknown, expired or already used; start the connection again"
	case http.StatusTooManyRequests:
		return "OrcaRouter refused the request; wait a moment and try again"
	default:
		return "OrcaRouter authorization failed"
	}
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("response too large")
	}
	return body, nil
}

// refuseRedirect keeps a credential-bearing request on the origin it was
// addressed to.
func refuseRedirect(*http.Request, []*http.Request) error {
	return errors.New("OrcaRouter redirects are not followed")
}

// reduceCatalogModel keeps the fields a model selector filters on and drops
// everything else. A record without a usable ID is refused rather than guessed.
func reduceCatalogModel(raw json.RawMessage) (catalogModel, bool) {
	var record struct {
		ID                    string          `json:"id"`
		Name                  string          `json:"name"`
		ContextLength         *int64          `json:"context_length"`
		MaxCompletionTokens   *int64          `json:"max_completion_tokens"`
		SupportedEndpointType []string        `json:"supported_endpoint_types"`
		Architecture          json.RawMessage `json:"architecture"`
	}
	if json.Unmarshal(raw, &record) != nil {
		return catalogModel{}, false
	}
	id := strings.TrimSpace(record.ID)
	if id == "" || len(id) > 200 || strings.ContainsAny(id, "\x00\r\n") {
		return catalogModel{}, false
	}
	name := strings.TrimSpace(record.Name)
	if name == "" {
		name = id
	}
	if len(name) > 200 {
		name = name[:200]
	}
	model := catalogModel{ID: id, Name: name, EndpointTypes: sanitizeStrings(record.SupportedEndpointType, 32, 64)}
	model.ContextLength = positiveInt64(record.ContextLength)
	model.MaxCompletion = positiveInt64(record.MaxCompletionTokens)
	var architecture struct {
		InputModalities []string `json:"input_modalities"`
	}
	if json.Unmarshal(record.Architecture, &architecture) == nil {
		model.InputModalities = sanitizeStrings(architecture.InputModalities, 16, 32)
		model.ModalitiesDeclared = architecture.InputModalities != nil
	}
	return model, true
}

func sanitizeStrings(values []string, maxItems, maxLength int) []string {
	if values == nil {
		return nil
	}
	out := make([]string, 0, min(len(values), maxItems))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || len(trimmed) > maxLength {
			continue
		}
		if len(out) >= maxItems {
			break
		}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func positiveInt64(value *int64) *int64 {
	if value == nil || *value <= 0 {
		return nil
	}
	return value
}
