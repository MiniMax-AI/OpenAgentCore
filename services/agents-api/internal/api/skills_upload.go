package api

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// readSkillUpload preserves directory paths from Content-Disposition. The
// multipart Part.FileName helper would discard their parent components.
func readSkillUpload(r *http.Request, versionUpload bool) ([]byte, bool, error) {
	if r.Header.Get("Content-Encoding") != "" {
		return nil, false, store.ErrInvalidInput
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, false, store.ErrInvalidInput
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	files, expanded := 0, 0
	var uploaded []byte
	var makeDefault, seenDefault, zipUpload bool
	paths := map[string]bool{}
	for {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, err
		}
		kind, attrs, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		field := attrs["name"]
		if err != nil || kind != "form-data" || part.Header.Get("Content-Transfer-Encoding") != "" {
			return nil, false, store.ErrInvalidInput
		}
		if field == "default" {
			if !versionUpload || seenDefault {
				return nil, false, store.ErrInvalidInput
			}
			if _, ok := attrs["filename"]; ok {
				return nil, false, store.ErrInvalidInput
			}
			value, err := io.ReadAll(io.LimitReader(part, 6))
			if err != nil || (string(value) != "true" && string(value) != "false") {
				return nil, false, store.ErrInvalidInput
			}
			seenDefault = true
			makeDefault = string(value) == "true"
		} else {
			name := attrs["filename"]
			if (field != "files" && field != "files[]") || name == "" || files >= 500 || zipUpload {
				return nil, false, store.ErrInvalidInput
			}
			if field == "files" {
				if files != 0 {
					return nil, false, store.ErrInvalidInput
				}
				uploaded, err = io.ReadAll(io.LimitReader(part, agentskill.MaxArchiveBytes+1))
				if err != nil {
					return nil, false, err
				}
				if len(uploaded) > agentskill.MaxArchiveBytes {
					return nil, false, store.ErrInvalidInput
				}
				zipUpload = true
			} else {
				if !utf8.ValidString(name) || len(name) > 4096 || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00\r\n") || !strings.Contains(name, "/") || paths[name] {
					return nil, false, store.ErrInvalidInput
				}
				paths[name] = true
				body, err := io.ReadAll(io.LimitReader(part, int64(agentskill.MaxExpandedBytes-expanded)+1))
				if err != nil {
					return nil, false, err
				}
				expanded += len(body)
				if expanded > agentskill.MaxExpandedBytes {
					return nil, false, store.ErrInvalidInput
				}
				header := &zip.FileHeader{Name: name, Method: zip.Deflate}
				header.SetMode(0644)
				file, err := writer.CreateHeader(header)
				if err != nil {
					return nil, false, err
				}
				if _, err = file.Write(body); err != nil {
					return nil, false, err
				}
			}
			files++
		}
		if err := part.Close(); err != nil {
			return nil, false, err
		}
	}
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		return nil, false, err
	}
	if err := writer.Close(); err != nil {
		return nil, false, err
	}
	if files == 0 {
		return nil, false, store.ErrInvalidInput
	}
	if !zipUpload {
		uploaded = archive.Bytes()
	}
	if _, err := agentskill.Inspect(uploaded); err != nil {
		return nil, false, store.ErrInvalidInput
	}
	return uploaded, makeDefault, nil
}
