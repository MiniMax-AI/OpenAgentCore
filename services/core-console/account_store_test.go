package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountStoreRejectsUnsafeOrCorruptState(t *testing.T) {
	for _, corruption := range []string{"invalid-json", "unknown-version", "bad-hash", "public-file", "symlink", "directory"} {
		t.Run(corruption, func(t *testing.T) {
			c := accountConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			h := accountConsole(t, c)
			setupAccount(t, h)
			name := filepath.Join(c.stateDir, accountFilename)
			switch corruption {
			case "invalid-json":
				_ = os.WriteFile(name, []byte("{"), 0o600)
			case "unknown-version", "bad-hash":
				body, _ := os.ReadFile(name)
				value := string(body)
				if corruption == "unknown-version" {
					value = strings.Replace(value, `"version":1`, `"version":2`, 1)
				} else {
					value = strings.Replace(value, "$2a$10$", "$2a$31$", 1)
				}
				_ = os.WriteFile(name, []byte(value), 0o600)
			case "public-file":
				_ = os.Chmod(name, 0o644)
			case "symlink":
				_ = os.Rename(name, name+".backup")
				_ = os.Symlink(name+".backup", name)
			case "directory":
				_ = os.Remove(name)
				_ = os.Mkdir(name, 0o700)
			}
			if reopened, err := newConsole(c); err == nil {
				reopened.Close()
				t.Fatal("invalid persisted administrator state was accepted")
			}
			w := authRequest(h, "GET", "/console/auth", "", nil)
			if w.Code != 503 || strings.Contains(w.Body.String(), `"setup"`) {
				t.Fatal("invalid state reopened registration")
			}
		})
	}
}

func TestAccountStoreDoesNotReopenSetupAfterLiveDeletion(t *testing.T) {
	c := accountConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h := accountConsole(t, c)
	setupAccount(t, h)
	if err := os.Remove(filepath.Join(c.stateDir, accountFilename)); err != nil {
		t.Fatal(err)
	}
	if w := authRequest(h, "GET", "/console/auth", "", nil); w.Code != 503 {
		t.Fatal("missing registered state reopened setup")
	}
	if w := authRequest(h, "POST", "/console/auth/setup", accountInput("other", testAccountPassword), nil); w.Code != 503 {
		t.Fatal("live state removal allowed administrator replacement")
	}
	if reopened, err := newConsole(c); err == nil {
		reopened.Close()
		t.Fatal("restart reopened setup after registered account state disappeared")
	}
}

func TestAccountConfigurationIsExplicitAndPrivate(t *testing.T) {
	work := t.TempDir()
	token := filepath.Join(work, "caller.key")
	if err := os.WriteFile(token, []byte("private-core-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(work, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORE_CONSOLE_TOKEN_FILE", token)
	t.Setenv("CORE_CONSOLE_PASSWORD_FILE", filepath.Join(work, "missing-password"))
	t.Setenv("CORE_CONSOLE_AUTH_MODE", "account")
	t.Setenv("CORE_CONSOLE_SETUP_KEY_FILE", filepath.Join(work, "obsolete-missing-setup-key"))
	t.Setenv("CORE_CONSOLE_STATE_DIR", state)
	t.Setenv("CORE_CONSOLE_DIST", work)
	t.Setenv("CORE_CONSOLE_ORIGIN", testOrigin)
	t.Setenv("CORE_CONSOLE_SANDBOX_ADMIN_TOKEN_FILE", "")
	if c, err := loadConfig(); err != nil || c.password != "" {
		t.Fatalf("valid account configuration rejected: %v", err)
	}
	for _, mode := range []string{"", "unknown"} {
		t.Run("mode="+mode, func(t *testing.T) {
			t.Setenv("CORE_CONSOLE_AUTH_MODE", mode)
			if _, err := loadConfig(); err == nil {
				t.Fatal("missing legacy password or unknown mode enabled registration")
			}
		})
	}
	for _, directory := range []string{"", "relative", filepath.Join(work, "missing"), token} {
		t.Run("directory="+directory, func(t *testing.T) {
			t.Setenv("CORE_CONSOLE_STATE_DIR", directory)
			if _, err := loadConfig(); err == nil {
				t.Fatal("invalid account directory accepted")
			}
		})
	}
	if err := os.Chmod(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("public account directory accepted")
	}
}
