package databaseurl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordComesOnlyFromTheFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "database.password")
	if err := os.WriteFile(file, []byte("s3cret/value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTS_API_DATABASE_PASSWORD_FILE", file)
	t.Setenv("AGENTS_API_DATABASE_URL", "postgres://agents_api@database:5432/agents_api?sslmode=disable")
	if got, err := FromEnvironment(); err != nil || got != "postgres://agents_api:s3cret%2Fvalue@database:5432/agents_api?sslmode=disable" {
		t.Fatal(got, err)
	}
	t.Setenv("AGENTS_API_DATABASE_URL", "postgres://agents_api:inline@database:5432/agents_api")
	if _, err := FromEnvironment(); err == nil || strings.Contains(err.Error(), "inline") {
		t.Fatal("accepted a password in two places", err)
	}
	t.Setenv("AGENTS_API_DATABASE_PASSWORD_FILE", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("AGENTS_API_DATABASE_URL", "postgres://agents_api@database:5432/agents_api")
	if _, err := FromEnvironment(); err == nil {
		t.Fatal("accepted a missing password file")
	}
	t.Setenv("AGENTS_API_DATABASE_PASSWORD_FILE", "")
	t.Setenv("AGENTS_API_DATABASE_URL", "postgres://agents_api:inline@database:5432/agents_api")
	if got, err := FromEnvironment(); err != nil || got != "postgres://agents_api:inline@database:5432/agents_api" {
		t.Fatal("standalone URL changed", got, err)
	}
}
