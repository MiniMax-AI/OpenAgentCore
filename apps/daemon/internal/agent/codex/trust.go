package codex

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeWorkspaceTrust marks the workspace as a trusted native project, the entry
// thread/start would persist, so start and cold resume load the same workspace
// configuration. The key is the nearest checkout root, else the workspace.
func writeWorkspaceTrust(codexHome, workspace string) error {
	root := workspace
	for dir := workspace; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			root = dir
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return appendConfigTOML(filepath.Join(codexHome, "config.toml"),
		fmt.Sprintf("[projects.%s]\ntrust_level = \"trusted\"\n\n", tomlQuoteString(root)))
}
