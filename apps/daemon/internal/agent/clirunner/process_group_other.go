//go:build !unix && !windows

package clirunner

import "errors"

func startProcessGroup(StartOptions) (*Process, error) {
	return nil, errors.New("clirunner: process ownership requires Unix or Windows")
}
