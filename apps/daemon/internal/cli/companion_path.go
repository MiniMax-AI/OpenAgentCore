package cli

import (
	"os"
	"path/filepath"
)

func addCompanionCLIPath(env map[string]any, dir string) {
	info, err := os.Stat(filepath.Join(dir, "parsar"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return
	}
	path, ok := env["PATH"].(string)
	if !ok {
		path = os.Getenv("PATH")
	}
	env["PATH"] = dir + string(os.PathListSeparator) + path
}
