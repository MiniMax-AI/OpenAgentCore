package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "core_console_session"
const sessionLifetime = 12 * time.Hour

type consoleSession struct {
	username string
	expires  time.Time
}

type consoleAuth struct {
	store    *accountStore
	secure   bool
	mu       sync.Mutex
	sessions map[[sha256.Size]byte]consoleSession
	attempts int
	window   time.Time
	workers  chan struct{}
}

func newConsoleAuth(c config) (*consoleAuth, error) {
	store, err := newAccountStore(c.stateDir)
	if err != nil {
		return nil, err
	}
	return &consoleAuth{store: store,
		secure: strings.HasPrefix(c.origin, "https://"), sessions: make(map[[sha256.Size]byte]consoleSession),
		workers: make(chan struct{}, 2)}, nil
}

func authJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func authError(w http.ResponseWriter, status int, message string) {
	authJSON(w, status, map[string]string{"error": message})
}

func cookieDigest(r *http.Request) ([sha256.Size]byte, bool) {
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == sessionCookie {
			value = cookie.Value
			count++
		}
	}
	if count != 1 || len(value) != 64 {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256([]byte(value)), true
}

func (a *consoleAuth) authenticated(r *http.Request) string {
	digest, ok := cookieDigest(r)
	if !ok {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[digest]
	if !ok || !time.Now().Before(session.expires) {
		delete(a.sessions, digest)
		return ""
	}
	return session.username
}

func (a *consoleAuth) setSession(w http.ResponseWriter, r *http.Request, username string) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		authError(w, http.StatusInternalServerError, "Cannot start a login session")
		return
	}
	value := hex.EncodeToString(token)
	now := time.Now()
	a.mu.Lock()
	if previous, ok := cookieDigest(r); ok {
		delete(a.sessions, previous)
	}
	for key, session := range a.sessions {
		if !now.Before(session.expires) {
			delete(a.sessions, key)
		}
	}
	// Keep memory bounded even when clients discard every login cookie.
	if len(a.sessions) >= 64 {
		var oldest [sha256.Size]byte
		expires := now.Add(sessionLifetime + time.Second)
		for key, session := range a.sessions {
			if session.expires.Before(expires) {
				oldest, expires = key, session.expires
			}
		}
		delete(a.sessions, oldest)
	}
	a.sessions[sha256.Sum256([]byte(value))] = consoleSession{username, now.Add(sessionLifetime)}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true,
		Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionLifetime.Seconds())})
	authJSON(w, http.StatusOK, map[string]string{"mode": "authenticated", "username": username})
}

func (a *consoleAuth) admitAttempt() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if now.Sub(a.window) >= time.Minute {
		a.window, a.attempts = now, 0
	}
	if a.attempts >= 10 {
		return false
	}
	a.attempts++
	return true
}

func (a *consoleAuth) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/console/auth" && r.Method == http.MethodGet {
		account, err := a.store.read()
		if err != nil {
			authError(w, http.StatusServiceUnavailable, "Administrator state is unavailable")
			return
		}
		if account == nil {
			authJSON(w, http.StatusOK, map[string]string{"mode": "setup"})
		} else if username := a.authenticated(r); username != "" {
			authJSON(w, http.StatusOK, map[string]string{"mode": "authenticated", "username": username})
		} else {
			authJSON(w, http.StatusOK, map[string]string{"mode": "login"})
		}
		return
	}
	if r.URL.Path != "/console/auth/setup" && r.URL.Path != "/console/auth/login" && r.URL.Path != "/console/auth/logout" {
		authError(w, http.StatusNotFound, "Unknown authentication route")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		authError(w, http.StatusMethodNotAllowed, "Use POST for this operation")
		return
	}
	if r.URL.Path == "/console/auth/logout" {
		if digest, ok := cookieDigest(r); ok {
			a.mu.Lock()
			delete(a.sessions, digest)
			a.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1,
			HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode})
		authJSON(w, http.StatusOK, map[string]string{"mode": "login"})
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		authError(w, http.StatusUnsupportedMediaType, "Use application/json")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		authError(w, http.StatusBadRequest, "Invalid authentication request")
		return
	}
	if !validUsername(input.Username) || len(input.Password) < 12 || len(input.Password) > 72 {
		authError(w, http.StatusBadRequest, "Use a 1–64 character username (letters, digits, ., _, -) and a 12–72 byte password")
		return
	}
	if !a.admitAttempt() {
		w.Header().Set("Retry-After", "60")
		authError(w, http.StatusTooManyRequests, "Too many authentication attempts; try again in one minute")
		return
	}
	select {
	case a.workers <- struct{}{}:
		defer func() { <-a.workers }()
	default:
		w.Header().Set("Retry-After", "1")
		authError(w, http.StatusTooManyRequests, "Authentication is busy; try again shortly")
		return
	}
	account, err := a.store.read()
	if err != nil {
		authError(w, http.StatusServiceUnavailable, "Administrator state is unavailable")
		return
	}
	if r.URL.Path == "/console/auth/setup" {
		if account != nil {
			authError(w, http.StatusConflict, "Administrator already registered; sign in")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if err != nil {
			authError(w, http.StatusInternalServerError, "Cannot register administrator")
			return
		}
		account = &administrator{Version: 1, Username: input.Username, PasswordHash: string(hash)}
		if err := a.store.create(*account); err != nil {
			if errors.Is(err, errAccountExists) {
				authError(w, http.StatusConflict, "Administrator already registered; sign in")
			} else {
				authError(w, http.StatusInternalServerError, "Cannot persist administrator; check the private state directory")
			}
			return
		}
	} else {
		if account == nil {
			authError(w, http.StatusConflict, "Register the administrator first")
			return
		}
		passwordErr := bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(input.Password))
		if passwordErr != nil || input.Username != account.Username {
			authError(w, http.StatusUnauthorized, "Invalid username or password")
			return
		}
	}
	a.setSession(w, r, account.Username)
}

func publicConsoleAsset(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return r.URL.Path == "/" || r.URL.Path == "/index.html" || r.URL.Path == "/favicon.png" ||
		r.URL.Path == "/parsar-mark-light.png" || r.URL.Path == "/parsar-mark-dark.png" || strings.HasPrefix(r.URL.Path, "/assets/")
}
