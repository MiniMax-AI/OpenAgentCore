package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/go-chi/chi/v5"
)

// CanonicalPaths serves each request on its canonical path, as the official
// service does (HP-17/HP-18), instead of the ServeMux redirect, which made
// clients resend a POST as a GET. It must wrap the complete server handler so
// that every routing, authentication and middleware decision sees only the
// rewritten path. The canonical path is built from the raw request path: bytes
// that are not valid in an escaped path are percent-encoded, percent-encoded
// unreserved characters (RFC 3986 section 2.3) are decoded, so an encoded dot
// segment resolves like a literal one, and empty and dot segments are resolved
// with ServeMux cleanPath semantics, keeping a trailing slash. Other escapes,
// such as %2F and %5C, stay encoded and never become separators. Path and
// RawPath are then set consistently, so chi (which prefers RawPath), the
// ServeMux (which uses EscapedPath) and every middleware see the same path, and
// every spelling reaches exactly the route and authentication of its canonical
// form written literally. It is idempotent.
func CanonicalPaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RawPath is the request's own spelling whenever it differs from the
		// default encoding of Path; EscapedPath would re-escape the decoded Path
		// when RawPath holds an invalid byte, turning %2F into a separator.
		raw := r.URL.RawPath
		if raw == "" {
			raw = r.URL.EscapedPath()
		}
		escaped := cleanPath(decodeUnreserved(escapeInvalid(raw)))
		decoded, err := url.PathUnescape(escaped)
		if err != nil {
			// A parsed request path has only valid escapes; this is unreachable.
			http.Error(w, "Invalid request path.", http.StatusBadRequest)
			return
		}
		rawPath := ""
		if (&url.URL{Path: decoded}).EscapedPath() != escaped {
			rawPath = escaped
		}
		if decoded != r.URL.Path || rawPath != r.URL.RawPath {
			canonical := *r.URL
			canonical.Path, canonical.RawPath = decoded, rawPath
			r = r.WithContext(r.Context())
			r.URL = &canonical
		}
		next.ServeHTTP(w, r)
	})
}

// escapeInvalid percent-encodes every byte that may not appear literally in an
// escaped path (RFC 3986 pchar, plus the '[' and ']' that net/url accepts), such
// as '{', '"', a backslash, spaces and non-ASCII bytes. Existing escapes are kept.
func escapeInvalid(raw string) string {
	var escaped strings.Builder
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; unreserved(c) || strings.IndexByte("!$&'()*+,;=:@/%[]", c) >= 0 {
			escaped.WriteByte(c)
		} else {
			fmt.Fprintf(&escaped, "%%%02X", c)
		}
	}
	return escaped.String()
}

// decodeUnreserved decodes percent-encoded ALPHA, DIGIT, '-', '.', '_' and '~',
// which RFC 3986 treats as equivalent to their literal form.
func decodeUnreserved(escaped string) string {
	if !strings.Contains(escaped, "%") {
		return escaped
	}
	var decoded strings.Builder
	decoded.Grow(len(escaped))
	for i := 0; i < len(escaped); i++ {
		if escaped[i] == '%' && i+2 < len(escaped) {
			if value, err := strconv.ParseUint(escaped[i+1:i+3], 16, 8); err == nil && unreserved(byte(value)) {
				decoded.WriteByte(byte(value))
				i += 2
				continue
			}
		}
		decoded.WriteByte(escaped[i])
	}
	return decoded.String()
}

func unreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

// cleanPath matches net/http ServeMux path cleaning: collapse empty segments,
// resolve dot segments and keep a trailing slash.
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	cleaned := path.Clean(p)
	if p[len(p)-1] == '/' && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

// agentsResponseHeaders adds the observed official response headers to every
// response of this handler, including errors, 401, 404, 405 and SSE streams
// (HP-23/HP-24): a fresh random X-Request-Id, attached to the request log
// context next to the trace carrier, OpenAI-Version, OpenAI-Processing-Ms at
// the time headers are written, and nosniff. Core's traceparent and
// Cache-Control extensions remain. Organization and project headers are not
// reported: Core's project scope is configured, not account-derived.
func agentsResponseHeaders(next http.Handler) http.Handler {
	return responseHeadersWithErrors(next, nil)
}

func responseHeadersWithErrors(next http.Handler, report func(string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		header := w.Header()
		header.Set("X-Request-Id", id)
		header.Set("Openai-Version", "2020-10-01")
		header.Set("X-Content-Type-Options", "nosniff")
		writer := &processingTimeWriter{ResponseWriter: w, started: time.Now(), report: report}
		next.ServeHTTP(writer, r.WithContext(log.WithRequestID(r.Context(), id)))
	})
}

func newRequestID() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return "req_" + hex.EncodeToString(id[:])
}

// processingTimeWriter reports the elapsed handling time when the final
// response headers are written, flushed or implied by the first body write.
// Unwrap keeps http.ResponseController deadlines and flushing available.
type processingTimeWriter struct {
	http.ResponseWriter
	started time.Time
	stamped bool
	report  func(string)
}

// reportAPIError observes the emitted code without reading or retaining bodies.
func (w *processingTimeWriter) reportAPIError(code string) {
	if !w.stamped && w.report != nil {
		w.report(code)
	}
}

func (w *processingTimeWriter) stamp() {
	if !w.stamped {
		w.stamped = true
		w.ResponseWriter.Header().Set("Openai-Processing-Ms", strconv.FormatInt(time.Since(w.started).Milliseconds(), 10))
	}
}

func (w *processingTimeWriter) WriteHeader(status int) {
	if status >= http.StatusOK {
		w.stamp()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *processingTimeWriter) Write(body []byte) (int, error) {
	w.stamp()
	return w.ResponseWriter.Write(body)
}

func (w *processingTimeWriter) FlushError() error {
	w.stamp()
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *processingTimeWriter) Flush() { _ = w.FlushError() }

func (w *processingTimeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// methodNotAllowed keeps Core's JSON 405 and adds the route's Allow header
// (HP-20). It also answers HEAD on routes that exclude it.
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	if allowed := allowedMethods(r); allowed != "" {
		w.Header().Set("Allow", allowed)
	}
	writeError(w, http.StatusMethodNotAllowed, "unsupported_operation", "This API method is not supported.")
}

// allowedMethods lists the methods routed for the request path in the official
// order (GET,HEAD,POST,DELETE). HEAD follows GET unless the route registers its
// own HEAD handler, which only ever excludes HEAD.
func allowedMethods(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil || rctx.Routes == nil {
		return ""
	}
	routePath := r.URL.RawPath
	if routePath == "" {
		routePath = r.URL.Path
	}
	routed := func(method string) bool {
		pattern := rctx.Routes.Find(chi.NewRouteContext(), method, routePath)
		if pattern == "" {
			return false
		}
		// chi's Find stops at the exact path of a mounted router, which serves
		// that path as its own "/" route.
		for _, route := range rctx.Routes.Routes() {
			if route.SubRoutes != nil && (pattern == strings.TrimSuffix(route.Pattern, "/*") || pattern == strings.TrimSuffix(route.Pattern, "*")) {
				return route.SubRoutes.Find(chi.NewRouteContext(), method, "/") != ""
			}
		}
		return true
	}
	var allowed []string
	if routed(http.MethodGet) {
		allowed = append(allowed, http.MethodGet)
		if !routed(http.MethodHead) {
			allowed = append(allowed, http.MethodHead)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if routed(method) {
			allowed = append(allowed, method)
		}
	}
	return strings.Join(allowed, ",")
}
