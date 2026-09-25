package main

import (
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type config struct {
	addr, origin, dist, password string
	adminToken, nodePayloadDir   string
	authMode, stateDir           string
	upstream                     *url.URL
}

func loadConfig() (config, error) {
	c := config{
		addr:   envDefault("CORE_CONSOLE_ADDR", ":8080"),
		origin: envDefault("CORE_CONSOLE_ORIGIN", "http://127.0.0.1:8080"),
		dist:   envDefault("CORE_CONSOLE_DIST", "/www"),
	}
	origin, err := serverURL(c.origin)
	if err != nil || origin.Path != "" {
		return config{}, errors.New("CORE_CONSOLE_ORIGIN must be an HTTP(S) origin without a path")
	}
	c.upstream, err = serverURL(envDefault("CORE_CONSOLE_UPSTREAM", "http://core:8091"))
	if err != nil {
		return config{}, errors.New("CORE_CONSOLE_UPSTREAM must be an HTTP(S) server URL without credentials, query or path")
	}
	if !filepath.IsAbs(c.dist) {
		return config{}, errors.New("CORE_CONSOLE_DIST must be absolute")
	}
	c.adminToken, err = readSecret(envDefault("CORE_CONSOLE_ADMIN_TOKEN_FILE", "/admin/sandbox-admin.key"))
	if err != nil {
		return config{}, errors.New("CORE_CONSOLE_ADMIN_TOKEN_FILE must name a private regular file containing one token")
	}
	c.authMode = os.Getenv("CORE_CONSOLE_AUTH_MODE")
	switch c.authMode {
	case "":
		c.password, err = readSecret(envDefault("CORE_CONSOLE_PASSWORD_FILE", "/config/console.password"))
		if err != nil {
			return config{}, errors.New("CORE_CONSOLE_PASSWORD_FILE must name a private regular file containing one password")
		}
		if c.password == c.adminToken {
			return config{}, errors.New("console password and administrator credential must differ")
		}
	case "account":
		c.stateDir = os.Getenv("CORE_CONSOLE_STATE_DIR")
		if err := validateAccountDirectory(c.stateDir); err != nil {
			return config{}, err
		}
	default:
		return config{}, errors.New("CORE_CONSOLE_AUTH_MODE must be account or unset for legacy Basic authentication")
	}
	c.nodePayloadDir = os.Getenv("CORE_CONSOLE_NODE_PAYLOAD_DIR")
	if c.nodePayloadDir != "" && (!filepath.IsAbs(c.nodePayloadDir) || c.adminToken == "") {
		return config{}, errors.New("node payload requires an absolute directory and paired administrator access")
	}
	return c, nil
}

func envDefault(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func serverURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.ContainsAny(value, "?#") || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return nil, errors.New("invalid server URL")
	}
	return u, nil
}

func readSecret(name string) (string, error) {
	if !filepath.IsAbs(name) {
		return "", errors.New("secret path must be absolute")
	}
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("secret file must be private and regular")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return "", errors.New("invalid secret size")
	}
	value := strings.TrimSpace(string(data))
	if value == "" || strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("invalid secret")
	}
	return value, nil
}
