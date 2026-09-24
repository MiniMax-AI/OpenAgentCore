package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const accountFilename = "admin.json"
const registeredFilename = "registered"

var errAccountExists = errors.New("administrator already registered")

type administrator struct {
	Version      int    `json:"version"`
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type accountStore struct {
	directory  string
	mu         sync.Mutex
	registered bool
}

func validateAccountDirectory(directory string) error {
	if !filepath.IsAbs(directory) {
		return errors.New("CORE_CONSOLE_STATE_DIR must be an absolute private directory")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("CORE_CONSOLE_STATE_DIR must be an existing private directory")
	}
	return nil
}

func newAccountStore(directory string) (*accountStore, error) {
	if err := validateAccountDirectory(directory); err != nil {
		return nil, err
	}
	s := &accountStore{directory: directory}
	if _, err := s.read(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *accountStore) read() (*administrator, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

func (s *accountStore) readLocked() (*administrator, error) {
	name := filepath.Join(s.directory, accountFilename)
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && !s.registered {
		if _, markerErr := os.Lstat(filepath.Join(s.directory, registeredFilename)); errors.Is(markerErr, os.ErrNotExist) {
			return nil, nil
		}
		return nil, errors.New("registered administrator state is missing")
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 4096 {
		return nil, errors.New("administrator state is unavailable or unsafe")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, errors.New("administrator state is unavailable")
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 4097))
	decoder.DisallowUnknownFields()
	var account administrator
	if decoder.Decode(&account) != nil || decoder.Decode(new(any)) != io.EOF || account.Version != 1 || !validUsername(account.Username) {
		return nil, errors.New("administrator state is invalid")
	}
	cost, err := bcrypt.Cost([]byte(account.PasswordHash))
	if err != nil || cost != bcrypt.DefaultCost {
		return nil, errors.New("administrator password state is invalid")
	}
	s.registered = true
	return &account, nil
}

func (s *accountStore) create(account administrator) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.readLocked()
	if err != nil {
		return err
	}
	if current != nil {
		return errAccountExists
	}
	f, err := os.CreateTemp(s.directory, ".admin-*")
	if err != nil {
		return errors.New("cannot create administrator state")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := json.NewEncoder(f).Encode(account); err != nil {
		return errors.New("cannot write administrator state")
	}
	if err := f.Sync(); err != nil {
		return errors.New("cannot persist administrator state")
	}
	// Link publishes a complete file atomically and never replaces another
	// process's winner. A crash before publication leaves only an unused temp file.
	if err := os.Link(f.Name(), filepath.Join(s.directory, accountFilename)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errAccountExists
		}
		return errors.New("cannot publish administrator state")
	}
	s.registered = true
	// Keep a durable consumed marker so losing just the account file never
	// silently reopens administrator registration.
	marker, err := os.OpenFile(filepath.Join(s.directory, registeredFilename), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("cannot persist registration marker")
	}
	markerErr := marker.Sync()
	closeErr := marker.Close()
	if markerErr != nil || closeErr != nil {
		return errors.New("cannot persist registration marker")
	}
	directory, err := os.Open(s.directory)
	if err != nil {
		return errors.New("cannot persist administrator directory")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return errors.New("cannot persist administrator directory")
	}
	return nil
}

func validUsername(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}
