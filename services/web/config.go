package main

import (
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type config struct {
	addr, origin, dist      string
	coreKey, nodePayloadDir string
	upstream                *url.URL
}

func loadConfig() (config, error) {
	c := config{
		addr:   envDefault("OAC_WEB_ADDR", ":8080"),
		origin: envDefault("OAC_WEB_ORIGIN", "http://127.0.0.1:8080"),
		dist:   envDefault("OAC_WEB_DIST", "/www"),
	}
	origin, err := serverURL(c.origin)
	if err != nil || origin.Path != "" {
		return config{}, errors.New("OAC_WEB_ORIGIN must be an HTTP(S) origin without a path")
	}
	c.upstream, err = serverURL(envDefault("OAC_WEB_UPSTREAM", "http://core:8091"))
	if err != nil {
		return config{}, errors.New("OAC_WEB_UPSTREAM must be an HTTP(S) server URL without credentials, query or path")
	}
	if !filepath.IsAbs(c.dist) {
		return config{}, errors.New("OAC_WEB_DIST must be absolute")
	}
	c.coreKey, err = readSecret(envDefault("OAC_WEB_CORE_KEY_FILE", "/admin/core.key"))
	if err != nil {
		return config{}, errors.New("OAC_WEB_CORE_KEY_FILE must name a private regular file containing the Core key")
	}
	if utf8.RuneCountInString(c.coreKey) < minimumCoreKeyLength {
		return config{}, errors.New("the Core key in OAC_WEB_CORE_KEY_FILE must have at least 32 characters")
	}
	c.nodePayloadDir = os.Getenv("OAC_WEB_NODE_PAYLOAD_DIR")
	if c.nodePayloadDir != "" && !filepath.IsAbs(c.nodePayloadDir) {
		return config{}, errors.New("OAC_WEB_NODE_PAYLOAD_DIR must be absolute")
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
