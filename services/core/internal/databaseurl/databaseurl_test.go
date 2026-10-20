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
	t.Setenv("OAC_DATABASE_PASSWORD_FILE", file)
	t.Setenv("OAC_DATABASE_URL", "postgres://oac@database:5432/oac?sslmode=disable")
	if got, err := FromEnvironment(); err != nil || got != "postgres://oac:s3cret%2Fvalue@database:5432/oac?sslmode=disable" {
		t.Fatal(got, err)
	}
	t.Setenv("OAC_DATABASE_URL", "postgres://oac:inline@database:5432/oac")
	if _, err := FromEnvironment(); err == nil || strings.Contains(err.Error(), "inline") {
		t.Fatal("accepted a password in two places", err)
	}
	t.Setenv("OAC_DATABASE_PASSWORD_FILE", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("OAC_DATABASE_URL", "postgres://oac@database:5432/oac")
	if _, err := FromEnvironment(); err == nil {
		t.Fatal("accepted a missing password file")
	}
	t.Setenv("OAC_DATABASE_PASSWORD_FILE", "")
	t.Setenv("OAC_DATABASE_URL", "postgres://oac:inline@database:5432/oac")
	if got, err := FromEnvironment(); err != nil || got != "postgres://oac:inline@database:5432/oac" {
		t.Fatal("standalone URL changed", got, err)
	}
}
