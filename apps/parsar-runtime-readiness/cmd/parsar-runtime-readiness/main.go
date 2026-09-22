// Command parsar-runtime-readiness serves the Runtime readiness endpoint that
// doubles as the Cube template probe (implementation specification §8.1).
//
// The default listen address is the fixed in-sandbox port; PARSAR_RUNTIME_READINESS_ADDR
// overrides it for a local image check. The endpoint answers 2xx only in the
// states readiness.Default() accepts, and its body carries a state word and
// nothing else: never a credential, a path or workspace content.
package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-runtime-readiness/internal/readiness"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

func main() {
	config := obslog.ConfigFromEnv()
	config.Level = slog.LevelInfo
	obslog.Init(config)
	address := os.Getenv("PARSAR_RUNTIME_READINESS_ADDR")
	if address == "" {
		address = net.JoinHostPort("0.0.0.0", strconv.Itoa(readiness.Port))
	}
	environment := readiness.Default()
	mux := http.NewServeMux()
	mux.HandleFunc(readiness.Path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		state := environment.Evaluate()
		w.Header().Set("Content-Type", "application/json")
		if !state.Ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(state)
	})
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	obslog.Bg().Info("runtime readiness endpoint listening", "addr", address, "path", readiness.Path)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		obslog.Bg().Error("runtime readiness endpoint stopped", "err", err)
		os.Exit(1)
	}
}
