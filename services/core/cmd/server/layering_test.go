package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestLayering checks the import rule of IMPLEMENTATION.md "Layering": outside
// internal/persistence and internal/db, no non-test file imports a persistence
// package, generated query or PostgreSQL driver.
func TestLayering(t *testing.T) {
	const root = "../../internal"
	const internal = "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/"
	forbidden := []string{internal + "persistence", internal + "db", "github.com/jackc/pgx"}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" || path == filepath.Join(root, "persistence") || path == filepath.Join(root, "db") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			for _, prefix := range forbidden {
				if imported == prefix || strings.HasPrefix(imported, prefix+"/") {
					t.Errorf("%s imports %s", path, imported)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
