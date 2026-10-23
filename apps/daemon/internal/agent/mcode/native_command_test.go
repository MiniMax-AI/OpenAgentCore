package mcode

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestNativeCommandRunsJavaScriptEntrypointWithNode(t *testing.T) {
	for _, test := range []struct {
		binary string
		want   []string
	}{
		{filepath.Join("opt", "native", "cli.js"), []string{"node22", filepath.Join("opt", "native", "cli.js")}},
		{filepath.Join("opt", "native", "CLI.JS"), []string{"node22", filepath.Join("opt", "native", "CLI.JS")}},
		{"mcode", []string{"mcode"}},
		{filepath.Join("bin", "mcode.exe"), []string{filepath.Join("bin", "mcode.exe")}},
	} {
		if got := nativeCommand(test.binary, "node22"); !slices.Equal(got, test.want) {
			t.Fatalf("nativeCommand(%q) = %q, want %q", test.binary, got, test.want)
		}
	}
}

// The Skill root links each installed Skill directory; on Windows this must
// work without Developer Mode or elevation.
func TestSelfHostedSkillDirectoryLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "installed", "skill")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("skill"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "skills", "skill")
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	if err := linkDirectory(target, link); err != nil {
		t.Fatal(err)
	}
	if actual, err := os.Readlink(link); err != nil || actual != target {
		t.Fatalf("Readlink = %q, %v; want %q", actual, err, target)
	}
	if raw, err := os.ReadFile(filepath.Join(link, "SKILL.md")); err != nil || string(raw) != "skill" {
		t.Fatalf("read through link = %q, %v", raw, err)
	}
	if err := linkDirectory(target, link); err == nil {
		t.Fatal("an existing link was replaced")
	}
	// Removing the Session's Skill root must leave the installed Skill intact.
	if err := os.RemoveAll(filepath.Dir(link)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("removing the link removed the target: %v", err)
	}
}
