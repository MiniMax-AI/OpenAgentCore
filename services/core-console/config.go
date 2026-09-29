package main

import (
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
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
	installationSocket      string
	bootstrap               bool
}

func loadConfig() (config, error) {
	var problems, renamed []string
	for _, setting := range [][2]string{
		{"CORE_CONSOLE_ADDR", "OAC_WEB_ADDR"},
		{"CORE_CONSOLE_ORIGIN", "OAC_WEB_ORIGIN"},
		{"CORE_CONSOLE_DIST", "OAC_WEB_DIST"},
		{"CORE_CONSOLE_UPSTREAM", "OAC_WEB_UPSTREAM"},
		{"CORE_CONSOLE_CORE_KEY_FILE", "OAC_WEB_CORE_KEY_FILE"},
		{"CORE_CONSOLE_NODE_PAYLOAD_DIR", "OAC_WEB_NODE_PAYLOAD_DIR"},
	} {
		if _, present := os.LookupEnv(setting[0]); present {
			renamed = append(renamed, setting[0]+" → "+setting[1])
		}
	}
	renamed = append(renamed, log.RenamedEnvironment()...)
	if len(renamed) > 0 {
		problems = append(problems, "OpenAgentCore renamed these Web settings; set the new names and remove the old ones: "+strings.Join(renamed, ", "))
	}
	if _, present := os.LookupEnv("CORE_CONSOLE_ADMIN_TOKEN_FILE"); present {
		problems = append(problems, "CORE_CONSOLE_ADMIN_TOKEN_FILE was renamed; set OAC_WEB_CORE_KEY_FILE to the Core key file instead")
	}
	for _, retired := range []string{"CORE_CONSOLE_AUTH_MODE", "CORE_CONSOLE_STATE_DIR", "CORE_CONSOLE_PASSWORD_FILE"} {
		if _, present := os.LookupEnv(retired); present {
			problems = append(problems, retired+" is retired; Web signs in with the Core key only, so remove this setting")
		}
	}
	if len(problems) > 0 {
		return config{}, errors.New(strings.Join(problems, "; "))
	}
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
	c.installationSocket = os.Getenv("OAC_WEB_INSTALLATION_SOCKET")
	if c.installationSocket != "" && !filepath.IsAbs(c.installationSocket) {
		return config{}, errors.New("OAC_WEB_INSTALLATION_SOCKET must be absolute")
	}
	bootstrap := envDefault("OAC_WEB_BOOTSTRAP", "0")
	if bootstrap != "0" && bootstrap != "1" {
		return config{}, errors.New("OAC_WEB_BOOTSTRAP must be 0 or 1")
	}
	c.bootstrap = bootstrap == "1"
	if c.bootstrap && (origin.Scheme != "http" || c.installationSocket == "") {
		return config{}, errors.New("HTTP bootstrap requires installation management and an HTTP origin")
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
