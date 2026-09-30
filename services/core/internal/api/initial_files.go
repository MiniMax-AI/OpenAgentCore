package api

import (
	"encoding/base64"
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func decodeInitialFiles(raw json.RawMessage) ([]environmentconfig.InitialFile, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) > 50 {
		return nil, store.ErrInvalidInput
	}
	files := make([]environmentconfig.InitialFile, 0, len(entries))
	for _, entry := range entries {
		var in struct {
			Type   string  `json:"type"`
			Path   string  `json:"path"`
			Data   *string `json:"data"`
			FileID *string `json:"file_id"`
		}
		if decodeInputObject(entry, &in, "type", "path", "data", "file_id") != nil {
			return nil, store.ErrInvalidInput
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(entry, &fields)
		f := environmentconfig.InitialFile{Type: in.Type, Path: in.Path}
		switch in.Type {
		case "inline":
			if _, exists := fields["file_id"]; exists || in.Data == nil || len(*in.Data) > base64.StdEncoding.EncodedLen(5<<20) {
				return nil, store.ErrInvalidInput
			}
			var err error
			f.Data, err = base64.StdEncoding.Strict().DecodeString(*in.Data)
			if err != nil {
				return nil, store.ErrInvalidInput
			}
		case "file_id":
			if _, exists := fields["data"]; exists || in.FileID == nil {
				return nil, store.ErrInvalidInput
			}
			f.FileID = *in.FileID
		default:
			return nil, store.ErrInvalidInput
		}
		files = append(files, f)
	}
	return files, environmentconfig.ValidateInitialFiles(files)
}

func initialFileResponse(files []environmentconfig.InitialFile) []json.RawMessage {
	return templateFileResponse(environmentconfig.InitialFilesMetadata(files))
}

func templateFileResponse(files []environmentconfig.InitialFileMetadata) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(files))
	for _, file := range files {
		body, _ := json.Marshal(file)
		result = append(result, body)
	}
	return result
}
