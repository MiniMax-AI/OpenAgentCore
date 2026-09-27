package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "core_console_session"
const sessionLifetime = 12 * time.Hour

// minimumCoreKeyLength keeps guessing infeasible even though a correct key is
// never rate limited. Installer-generated keys have 64 characters.
const minimumCoreKeyLength = 32

// consoleAuth signs the browser in with the Core key. Sessions live only in
// memory, so a console restart or Core key rotation requires signing in again.
type consoleAuth struct {
	coreKey  [sha256.Size]byte
	secure   bool
	mu       sync.Mutex
	sessions map[[sha256.Size]byte]time.Time
	attempts int
	window   time.Time
	workers  chan struct{}
}

func newConsoleAuth(c config) *consoleAuth {
	return &consoleAuth{coreKey: sha256.Sum256([]byte(c.coreKey)),
		secure: strings.HasPrefix(c.origin, "https://"), sessions: make(map[[sha256.Size]byte]time.Time),
		workers: make(chan struct{}, 2)}
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

func (a *consoleAuth) authenticated(r *http.Request) bool {
	digest, ok := cookieDigest(r)
	if !ok {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	expires, ok := a.sessions[digest]
	if !ok || !time.Now().Before(expires) {
		delete(a.sessions, digest)
		return false
	}
	return true
}

func (a *consoleAuth) setSession(w http.ResponseWriter, r *http.Request) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		authError(w, http.StatusServiceUnavailable, "Sign-in is unavailable; try again later")
		return
	}
	value := hex.EncodeToString(token)
	now := time.Now()
	a.mu.Lock()
	if previous, ok := cookieDigest(r); ok {
		delete(a.sessions, previous)
	}
	for key, expires := range a.sessions {
		if !now.Before(expires) {
			delete(a.sessions, key)
		}
	}
	// Keep memory bounded even when clients discard every login cookie.
	if len(a.sessions) >= 64 {
		var oldest [sha256.Size]byte
		earliest := now.Add(sessionLifetime + time.Second)
		for key, expires := range a.sessions {
			if expires.Before(earliest) {
				oldest, earliest = key, expires
			}
		}
		delete(a.sessions, oldest)
	}
	a.sessions[sha256.Sum256([]byte(value))] = now.Add(sessionLifetime)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true,
		Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionLifetime.Seconds())})
	authJSON(w, http.StatusOK, map[string]string{"mode": "authenticated"})
}

// admitFailure charges one failed sign-in to the shared budget. Correct keys are
// never charged, so failed attempts cannot lock out the key holder.
func (a *consoleAuth) admitFailure() bool {
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
		if a.authenticated(r) {
			authJSON(w, http.StatusOK, map[string]string{"mode": "authenticated"})
		} else {
			authJSON(w, http.StatusOK, map[string]string{"mode": "login"})
		}
		return
	}
	if r.URL.Path != "/console/auth/login" && r.URL.Path != "/console/auth/logout" {
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
	// Decode members by exact name so case variants count as unknown fields.
	var input map[string]json.RawMessage
	var coreKey string
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || len(input) != 1 ||
		json.Unmarshal(input["core_key"], &coreKey) != nil || coreKey == "" {
		authError(w, http.StatusBadRequest, "Send a JSON object with only a non-empty core_key")
		return
	}
	select {
	case a.workers <- struct{}{}:
		defer func() { <-a.workers }()
	default:
		w.Header().Set("Retry-After", "1")
		authError(w, http.StatusTooManyRequests, "Sign-in is busy; try again shortly")
		return
	}
	// Comparing fixed-size digests keeps the check constant-time for any key length.
	submitted := sha256.Sum256([]byte(coreKey))
	if subtle.ConstantTimeCompare(submitted[:], a.coreKey[:]) == 1 {
		a.setSession(w, r)
		return
	}
	if !a.admitFailure() {
		w.Header().Set("Retry-After", "60")
		authError(w, http.StatusTooManyRequests, "Too many failed sign-in attempts; try again in one minute")
		return
	}
	authError(w, http.StatusUnauthorized, "Invalid Core key")
}

func publicConsoleAsset(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return r.URL.Path == "/" || r.URL.Path == "/index.html" || r.URL.Path == "/favicon.png" ||
		r.URL.Path == "/oac-mark-light.png" || r.URL.Path == "/oac-mark-dark.png" || strings.HasPrefix(r.URL.Path, "/assets/")
}
