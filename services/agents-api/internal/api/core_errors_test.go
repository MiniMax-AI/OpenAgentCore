package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/go-chi/chi/v5"
)

func TestCoreErrorDetailsAreScopedByRouterNotRequestPath(t *testing.T) {
	shared := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCoreError(w, 409, "sandbox_generation_stale", "Refresh the configuration.", CoreErrorDetails{
			"current_generation": CoreErrorNumber(4), "kinds": CoreErrorStrings("docker", "microsandbox"),
		}, "expected_generation")
	})
	router := chi.NewRouter()
	admin, err := NewDeploymentAuthenticator([]string{device.HashCredential("admin")})
	if err != nil {
		t.Fatal(err)
	}
	(&Handler{deploymentAuth: admin}).registerCoreRoutes(router)
	var core chi.Router
	for _, route := range router.Routes() {
		if route.Pattern == "/core/v1/*" {
			core, _ = route.SubRoutes.(chi.Router)
		}
	}
	if core == nil {
		t.Fatal("Core router is missing")
	}
	core.Handle("/test", shared)
	router.Handle("/v1/test", shared)
	router.Handle("/api/v1/test", shared)
	golden := "{\"error\":{\"message\":\"Refresh the configuration.\",\"type\":\"conflict_error\",\"code\":\"sandbox_generation_stale\",\"param\":\"expected_generation\"}}\n"
	for _, path := range []string{"/core/v1/test", "/v1/test", "/api/v1/test"} {
		out := httptest.NewRecorder()
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("Authorization", "Bearer admin")
		router.ServeHTTP(out, request)
		if out.Code != 409 || out.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, out.Code, out.Header())
		}
		if path != "/core/v1/test" {
			if out.Body.String() != golden {
				t.Fatalf("%s changed public/machine error: %s", path, out.Body)
			}
		} else {
			var body map[string]map[string]any
			if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"current_generation": float64(4), "kinds": []any{"docker", "microsandbox"}}
			if !reflect.DeepEqual(body["error"]["details"], want) {
				t.Fatal(body)
			}
		}
	}
	for _, path := range []string{"/core/v1/test", "/core/v1/sandbox/unknown"} {
		out := httptest.NewRecorder()
		router.ServeHTTP(out, httptest.NewRequest("GET", path, nil))
		if out.Code != 401 || !strings.Contains(out.Body.String(), `"code":"invalid_admin_key"`) || strings.Contains(out.Body.String(), `"details"`) {
			t.Fatal("authentication did not precede operation", path, out.Code, out.Body)
		}
	}
	// A Core-looking path cannot itself authorize the optional field.
	out := httptest.NewRecorder()
	shared.ServeHTTP(out, httptest.NewRequest("GET", "/core/v1/test", nil))
	if out.Body.String() != golden {
		t.Fatal("request path bypassed the router mark", out.Body)
	}
}

func TestCoreDetailsTypesOmissionAndSnapshot(t *testing.T) {
	values := []string{"docker"}
	stringsDetail := CoreErrorStrings(values...)
	values[0] = "private-request-value"
	valid := CoreErrorDetails{"label": CoreErrorString("ready"), "number": CoreErrorNumber(1.5), "enabled": CoreErrorBoolean(true), "unset": CoreErrorNull(), "values": stringsDetail, "empty": CoreErrorStrings()}
	for _, details := range []CoreErrorDetails{nil, {}, {"bad": CoreErrorNumber(math.NaN())}, {"bad": CoreErrorNumber(math.Inf(1))}, {"bad": CoreErrorDetail{map[string]string{"nested": "private"}}}, {"": CoreErrorString("private")}, valid} {
		out := httptest.NewRecorder()
		coreErrorResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeCoreError(w, 400, "invalid_request", "Invalid request.", details)
		})).ServeHTTP(out, httptest.NewRequest("GET", "/core/v1/test", nil))
		var body map[string]map[string]any
		if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		_, present := body["error"]["details"]
		if present != (len(details) == len(valid)) || strings.Contains(out.Body.String(), "private") {
			t.Fatal(out.Body)
		}
	}
}

type detailDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *detailDeadlineWriter) SetWriteDeadline(value time.Time) error {
	w.deadline = value
	return nil
}

func TestCoreMarkerPreservesObservationAndStreamingWrappers(t *testing.T) {
	for _, markerOutside := range []bool{true, false} {
		out := &detailDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
		var observed []string
		handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deadline := time.Unix(1234, 0)
			if err := http.NewResponseController(w).SetWriteDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			writeCoreError(w, 503, "execution_unavailable", "Unavailable.", CoreErrorDetails{"retryable": CoreErrorBoolean(false)})
			if f, ok := w.(http.Flusher); !ok {
				t.Fatal("Flusher was lost")
			} else {
				f.Flush()
			}
			writeError(w, 503, "execution_unavailable", "After headers.")
		}))
		observer := func(code string) { observed = append(observed, code) }
		if markerOutside {
			handler = coreErrorResponses(responseHeadersWithErrors(handler, observer))
		} else {
			handler = responseHeadersWithErrors(coreErrorResponses(handler), observer)
		}
		handler.ServeHTTP(out, httptest.NewRequest("GET", "/core/v1/test", nil))
		if !out.Flushed || !out.deadline.Equal(time.Unix(1234, 0)) || out.Header().Get("Openai-Processing-Ms") == "" || !reflect.DeepEqual(observed, []string{"execution_unavailable"}) || !strings.Contains(out.Body.String(), `"details":{"retryable":false}`) {
			t.Fatal(markerOutside, out, observed)
		}
	}
}
