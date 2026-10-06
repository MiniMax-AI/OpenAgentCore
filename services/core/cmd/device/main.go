// Command device provisions or revokes an execution device using operator DB access.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/databaseurl"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func main() {
	if err := run(); err != nil {
		log.Bg().Error("execution device provisioning failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	tenant := flag.String("tenant", "", "execution tenant UUID")
	name := flag.String("name", "", "device label")
	serverURL := flag.String("url", "", "Agents API HTTP base URL")
	revoke := flag.String("revoke", "", "revoke this device UUID instead of provisioning")
	flag.Parse()
	dsn, err := databaseurl.FromEnvironment()
	if err != nil {
		return err
	}
	if dsn == "" || *tenant == "" || flag.NArg() != 0 {
		return errors.New("OAC_DATABASE_URL and --tenant are required")
	}
	if *revoke == "" {
		u, err := url.Parse(*serverURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("--url must be an absolute http(s) base URL without a path or credentials")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("invalid execution database configuration")
	}
	defer pool.Close()
	// Device provisioning opens no frozen Session data, so it needs no
	// credential key.
	s, err := sessions.NewService(sessionpg.New(pgunit.NewPool(pool), nil))
	if err != nil {
		return err
	}
	if *revoke != "" {
		return s.RevokeDevice(ctx, *tenant, *revoke)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	credential := base64.RawURLEncoding.EncodeToString(secret)
	registered, err := s.CreateDevice(ctx, *tenant, *name, runtimedevice.HashCredential(credential))
	if err != nil {
		return err
	}
	// Emit the existing daemon profile shape. Operators redirect this secret to a
	// mode-0600 auth.json under ~/.oac; it is never included in diagnostic logs.
	return json.NewEncoder(os.Stdout).Encode(map[string]string{
		"server_url": strings.TrimRight(*serverURL, "/") + "/api/v1", "runtime_id": registered.ID,
		"runner_credential": credential, "device_name": registered.Name,
	})
}
