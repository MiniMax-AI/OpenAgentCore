//go:build windows

package localworkspace

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestWindowsPackageManagerResolution(t *testing.T) {
	installation := filepath.Join(t.TempDir(), "selected node")
	if err := os.MkdirAll(filepath.Join(installation, "node_modules", "npm", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node.exe", "npm.cmd", "npx.cmd", "node_modules/npm/bin/npm-cli.js", "node_modules/npm/bin/npx-cli.js"} {
		if err := os.WriteFile(filepath.Join(installation, filepath.FromSlash(name)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"two words", "literal & | < > ^ %PATH%", "quote\"value"}
	for _, name := range []string{"npm", "npx"} {
		command := filepath.Join(installation, name+".cmd")
		binary, got, err := ResolvePackageManagerCommand(command, args)
		want := append([]string{filepath.Join(installation, "node_modules", "npm", "bin", name+"-cli.js")}, args...)
		if err != nil || binary != filepath.Join(installation, "node.exe") || !slices.Equal(got, want) {
			t.Fatal(binary, got, err)
		}
	}
	for _, name := range []string{"server.exe", "npm.exe", "other.cmd"} {
		binary, got, err := ResolvePackageManagerCommand(name, args)
		if err != nil || binary != name || !slices.Equal(got, args) {
			t.Fatal("ordinary command changed", binary, got, err)
		}
	}
	if err := os.Remove(filepath.Join(installation, "node_modules", "npm", "bin", "npx-cli.js")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolvePackageManagerCommand(filepath.Join(installation, "npx.cmd"), args); err == nil {
		t.Fatal("missing selected CLI accepted")
	}
}

func TestWindowsPackageManagerArguments(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("native test requires installed Node.js", err)
	}
	installation := filepath.Join(t.TempDir(), "npm installation")
	directory := filepath.Join(installation, "node_modules", "npm", "bin")
	if err = os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", installation+string(os.PathListSeparator)+os.Getenv("PATH"))
	args := []string{"two words", "literal & | < > ^ %PATH%", "quote\"value"}
	for _, name := range []string{"npm", "npx"} {
		if err = os.WriteFile(filepath.Join(installation, name+".cmd"), []byte("@exit /b 99\r\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, name+"-cli.js"), []byte("process.stdout.write(JSON.stringify(process.argv.slice(2)))"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{name, name + ".cmd", filepath.Join(installation, name+".cmd")} {
			binary, argv, err := ResolvePackageManagerCommand(command, args)
			if err != nil || filepath.Clean(binary) != filepath.Clean(node) {
				t.Fatal("PATH Node fallback failed", err)
			}
			out, err := exec.CommandContext(t.Context(), binary, argv...).Output()
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			if json.Unmarshal(out, &got) != nil || !slices.Equal(got, args) {
				t.Fatal("arguments were interpreted", string(out))
			}
		}
	}
}
