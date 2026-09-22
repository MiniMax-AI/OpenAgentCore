//go:build !unix

package readiness

// alive always reports false off Unix: this endpoint only ever serves inside a
// Linux sandbox, and a portability stub must never claim a process is running.
func alive(int) bool { return false }
