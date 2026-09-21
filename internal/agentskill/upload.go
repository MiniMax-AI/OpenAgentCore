package agentskill

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
)

// Inspect obtains portable metadata from an uploaded bundle, then applies the
// same complete archive validation used for environment installation.
func Inspect(archive []byte) (Metadata, error) {
	if len(archive) > MaxArchiveBytes {
		return Metadata{}, ErrInvalid
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(reader.File) > MaxFiles {
		return Metadata{}, ErrInvalid
	}
	var metadata Metadata
	found := false
	files := 0
	for _, entry := range reader.File {
		if !entry.FileInfo().IsDir() {
			files++
			if files > 500 {
				return Metadata{}, ErrInvalid
			}
		}
		parts := strings.Split(entry.Name, "/")
		if len(parts) != 2 || !strings.EqualFold(parts[1], "SKILL.md") {
			continue
		}
		if found || entry.UncompressedSize64 > 256<<10 {
			return Metadata{}, ErrInvalid
		}
		stream, err := entry.Open()
		if err != nil {
			return Metadata{}, ErrInvalid
		}
		body, readErr := io.ReadAll(io.LimitReader(stream, (256<<10)+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil {
			return Metadata{}, ErrInvalid
		}
		metadata, err = manifestMetadata(body)
		if err != nil {
			return Metadata{}, err
		}
		found = true
	}
	if !found {
		return Metadata{}, ErrInvalid
	}
	if _, err = Read(archive, metadata); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}
