// Command oac-web serves the Core Web build and its authenticated API proxy.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "core-key" {
		if err := printCoreKey(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		log.Bg().Error("Core console stopped", "error", err)
		os.Exit(1)
	}
}

// printCoreKey writes the sign-in key for `docker compose exec web oac-web
// core-key`. Exec output never enters the container log.
func printCoreKey() error {
	key, err := readSecret(envDefault("OAC_WEB_CORE_KEY_FILE", "/admin/core.key"))
	if err != nil {
		return errors.New("cannot read the Core key from OAC_WEB_CORE_KEY_FILE")
	}
	fmt.Println(key)
	return nil
}

// healthcheck reports the installation healthy once Core and Web's own listener
// answer. Compose runs it inside the web container.
func healthcheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	for _, host := range []string{"core:8091", "127.0.0.1:8080"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/healthz", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("%s returned HTTP %d", host, response.StatusCode)
		}
	}
	return nil
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
