package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/databaseurl"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/migrations"
)

func main() {
	if err := run(); err != nil {
		log.Bg().Error("oac-core migration failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL, err := databaseurl.FromEnvironment()
	if err != nil {
		return err
	}
	if databaseURL == "" {
		return errors.New("OAC_DATABASE_URL must point to a dedicated execution database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return migrations.Apply(ctx, databaseURL)
}
