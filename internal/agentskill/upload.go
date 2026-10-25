package agentskill

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Inspect obtains portable metadata from an uploaded bundle, then applies the
// same complete archive validation used for environment installation.
func Inspect(archive []byte) (proto.SkillMetadata, error) {
	if len(archive) > MaxArchiveBytes {
		return proto.SkillMetadata{}, ErrInvalid
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(reader.File) > MaxFiles {
		return proto.SkillMetadata{}, ErrInvalid
	}
	var metadata proto.SkillMetadata
	found := false
	files := 0
	for _, entry := range reader.File {
		if !entry.FileInfo().IsDir() {
			files++
			if files > 500 {
				return proto.SkillMetadata{}, ErrInvalid
			}
		}
		parts := strings.Split(entry.Name, "/")
		if len(parts) != 2 || !strings.EqualFold(parts[1], "SKILL.md") {
			continue
		}
		if found || entry.UncompressedSize64 > 256<<10 {
			return proto.SkillMetadata{}, ErrInvalid
		}
		stream, err := entry.Open()
		if err != nil {
			return proto.SkillMetadata{}, ErrInvalid
		}
		body, readErr := io.ReadAll(io.LimitReader(stream, (256<<10)+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil {
			return proto.SkillMetadata{}, ErrInvalid
		}
		metadata, err = manifestMetadata(body)
		if err != nil {
			return proto.SkillMetadata{}, err
		}
		found = true
	}
	if !found {
		return proto.SkillMetadata{}, ErrInvalid
	}
	if _, err = Read(archive, metadata); err != nil {
		return proto.SkillMetadata{}, err
	}
	return metadata, nil
}
