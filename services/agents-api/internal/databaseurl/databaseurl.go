// Package databaseurl reads Core's PostgreSQL connection settings, shared by
// the server and its maintenance commands.
package databaseurl

import (
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
)

// FromEnvironment returns AGENTS_API_DATABASE_URL. When
// AGENTS_API_DATABASE_PASSWORD_FILE is set, the password comes only from that
// file and the URL must not contain one. An empty result means the URL is
// unset; each command reports that in its own terms.
func FromEnvironment() (string, error) {
	raw := os.Getenv("AGENTS_API_DATABASE_URL")
	file := os.Getenv("AGENTS_API_DATABASE_PASSWORD_FILE")
	if raw == "" || file == "" {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.User.Username() == "" {
		return "", errors.New("AGENTS_API_DATABASE_PASSWORD_FILE requires AGENTS_API_DATABASE_URL in postgres:// form with a user name")
	}
	if _, set := u.User.Password(); set {
		return "", errors.New("set the database password only in AGENTS_API_DATABASE_PASSWORD_FILE, not also in AGENTS_API_DATABASE_URL")
	}
	password, err := readPassword(file)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword(u.User.Username(), password)
	return u.String(), nil
}

func readPassword(path string) (string, error) {
	failure := errors.New("AGENTS_API_DATABASE_PASSWORD_FILE must name a readable regular file containing one password line")
	f, err := os.Open(path)
	if err != nil {
		return "", failure
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", failure
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return "", failure
	}
	password := strings.TrimSpace(string(data))
	if password == "" || strings.ContainsAny(password, "\x00\r\n") {
		return "", failure
	}
	return password, nil
}
