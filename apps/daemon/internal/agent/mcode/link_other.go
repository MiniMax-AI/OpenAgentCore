//go:build !windows

package mcode

import "os"

// linkDirectory makes link resolve to the existing directory target.
func linkDirectory(target, link string) error {
	return os.Symlink(target, link)
}
