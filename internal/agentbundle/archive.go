// Package agentbundle validates bounded regular-file capability archives.
package agentbundle

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	MaxArchiveBytes  = 5 << 20
	MaxExpandedBytes = 20 << 20
	MaxFiles         = 1000
)

var ErrInvalid = errors.New("invalid or unsupported capability archive")

type File struct {
	Path       string `json:"path"`
	Data       []byte `json:"data"`
	Executable bool   `json:"executable,omitempty"`
}

// Read strips a single archive root after validating every member and its bounds.
func Read(archive []byte) ([]File, error) {
	if len(archive) > MaxArchiveBytes {
		return nil, ErrInvalid
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(reader.File) == 0 || len(reader.File) > MaxFiles {
		return nil, ErrInvalid
	}
	root := ""
	seen := map[string]bool{}
	files := []File{}
	total := 0
	for _, entry := range reader.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if !utf8.ValidString(name) || len(name) > 4096 || strings.ContainsAny(name, "\\\x00\r\n") || path.Clean(name) != name || path.IsAbs(name) {
			return nil, ErrInvalid
		}
		parts := strings.SplitN(name, "/", 2)
		if parts[0] == "." || parts[0] == ".." || parts[0] == "" {
			return nil, ErrInvalid
		}
		if root == "" {
			root = parts[0]
		}
		if root != parts[0] || seen[name] || entry.Flags&1 != 0 {
			return nil, ErrInvalid
		}
		seen[name] = true
		if entry.Mode().Type() == os.ModeDir {
			continue
		}
		if !entry.Mode().IsRegular() || len(parts) != 2 || entry.UncompressedSize64 > MaxExpandedBytes || total+int(entry.UncompressedSize64) > MaxExpandedBytes {
			return nil, ErrInvalid
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, ErrInvalid
		}
		body, readErr := io.ReadAll(io.LimitReader(stream, int64(MaxExpandedBytes-total)+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || len(body) > MaxExpandedBytes-total {
			return nil, ErrInvalid
		}
		total += len(body)

		files = append(files, File{Path: parts[1], Data: body, Executable: entry.Mode().Perm()&0111 != 0})
	}

	regular := map[string]bool{}
	for _, f := range files {
		regular[f.Path] = true
	}
	for _, f := range files {
		for parent := path.Dir(f.Path); parent != "."; parent = path.Dir(parent) {
			if regular[parent] {
				return nil, ErrInvalid
			}
		}
	}
	return files, nil
}
