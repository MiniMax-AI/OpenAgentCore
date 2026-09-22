//go:build !linux

package cli

import "errors"

func suspendProcessStart(int) (string, error) {
	return "", errors.New("hosted suspension requires Linux")
}
func signalSuspendedProcess(suspendIdentity) error {
	return errors.New("hosted suspension requires Linux")
}
