//go:build linux

package worldfs

// ScopeErr exposes unavailable scope diagnostics to external package tests.
func (w *World) ScopeErr() error { return w.fs.scopeErr }
