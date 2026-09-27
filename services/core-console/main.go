// Command oac-web serves the Core Web build and its authenticated API proxy.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

func main() {
	if err := run(); err != nil {
		log.Bg().Error("Core console stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	log.Init(log.ConfigFromEnv())
	c, err := loadConfig()
	if err != nil {
		return err
	}
	handler, err := newConsole(c)
	if err != nil {
		return err
	}
	defer handler.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: c.addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("console listener failed")
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return server.Close()
		}
		return nil
	}
}
