package api

import (
	"encoding/json"
	"math"
	"net/http"
)

// CoreErrorResponse is the administration error envelope. Public and machine
// routes retain v1.ErrorResponse and never emit Details.
type CoreErrorResponse struct {
	Error CoreAPIError `json:"error" binding:"required"`
}

type CoreAPIError struct {
	Message string  `json:"message" binding:"required"`
	Type    string  `json:"type" binding:"required"`
	Code    *string `json:"code" extensions:"x-nullable"`
	Param   *string `json:"param" extensions:"x-nullable"`
	// Details contains only documented, Core-owned facts: string, finite number,
	// boolean, null or string array values. Never include request echoes, secrets
	// or native/provider error text. Empty or invalid details are omitted.
	Details CoreErrorDetails `json:"details,omitempty" swaggertype:"object"`
}

type CoreErrorDetails map[string]CoreErrorDetail

// CoreErrorDetail has a closed set of constructors so nested objects, arbitrary
// errors and provider responses cannot be passed as detail values. The zero
// value represents JSON null. Callers must choose documented fixed keys and
// safe facts; a string constructor is not a redaction mechanism.
type CoreErrorDetail struct{ value any }

func CoreErrorString(value string) CoreErrorDetail  { return CoreErrorDetail{value} }
func CoreErrorNumber(value float64) CoreErrorDetail { return CoreErrorDetail{value} }
func CoreErrorNull() CoreErrorDetail                { return CoreErrorDetail{} }
func CoreErrorStrings(values ...string) CoreErrorDetail {
	return CoreErrorDetail{append([]string{}, values...)}
}

func (value CoreErrorDetail) MarshalJSON() ([]byte, error) { return json.Marshal(value.value) }

func validCoreDetails(details CoreErrorDetails) CoreErrorDetails {
	if len(details) == 0 {
		return nil
	}
	for key, detail := range details {
		if key == "" {
			return nil
		}
		switch value := detail.value.(type) {
		case nil, string, []string:
		case float64:
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil
			}
		default:
			return nil
		}
	}
	return details
}

// writeCoreError is for fixed administration diagnostics, not native error
// text. The router mark, rather than a caller's path or choice of helper,
// determines whether details can be emitted.
func writeCoreError(w http.ResponseWriter, status int, code, message string, details CoreErrorDetails, param ...string) {
	writeAPIError(w, status, code, message, details, param...)
}

func coreErrorResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&coreErrorWriter{w}, r)
	})
}

type coreErrorWriter struct{ http.ResponseWriter }

func (*coreErrorWriter) coreErrorResponse()            {}
func (w *coreErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *coreErrorWriter) FlushError() error {
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *coreErrorWriter) Flush()                     { _ = w.FlushError() }
func (w *coreErrorWriter) reportAPIError(code string) { reportAPIError(w.ResponseWriter, code) }

func isCoreErrorWriter(w http.ResponseWriter) bool {
	for w != nil {
		if _, ok := w.(interface{ coreErrorResponse() }); ok {
			return true
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		w = unwrapper.Unwrap()
	}
	return false
}

func reportAPIError(w http.ResponseWriter, code string) {
	for w != nil {
		if observer, ok := w.(interface{ reportAPIError(string) }); ok {
			observer.reportAPIError(code)
			return
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = unwrapper.Unwrap()
	}
}
