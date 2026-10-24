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

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
)

// readSkillUpload preserves directory paths from Content-Disposition. The
// multipart Part.FileName helper would discard their parent components.
func readSkillUpload(r *http.Request, versionUpload bool) ([]byte, bool, error) {
	if r.Header.Get("Content-Encoding") != "" {
		return nil, false, invalidFiles("Skill uploads do not support Content-Encoding.")
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, false, invalidFiles("Skill uploads must be multipart/form-data.")
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
			return nil, false, invalidFiles("Each part must be form-data without a Content-Transfer-Encoding.")
		}
		if field == "default" {
			if !versionUpload {
				return nil, false, &fieldError{param: "default", message: "Unknown parameter: 'default'."}
			}
			if _, ok := attrs["filename"]; ok || seenDefault {
				return nil, false, &fieldError{param: "default", message: "The 'default' field must be one plain value."}
			}
			value, err := io.ReadAll(io.LimitReader(part, 6))
			if err != nil {
				return nil, false, err
			}
			makeDefault = strings.EqualFold(string(value), "true")
			if !makeDefault && !strings.EqualFold(string(value), "false") {
				return nil, false, invalidValue("default", string(value), []string{"true", "false"})
			}
			seenDefault = true
		} else {
			name := attrs["filename"]
			if field != "files" && field != "files[]" {
				if !echoableField(field) {
					return nil, false, errUnknownParameter
				}
				return nil, false, &fieldError{param: field, message: "Unknown parameter: '" + field + "'."}
			}
			if name == "" {
				return nil, false, invalidFiles("Each 'files' part must be a file with a filename.")
			}
			if files >= 500 || zipUpload {
				return nil, false, invalidFiles("Upload either one ZIP file or at most 500 files, not both.")
			}
			if field == "files" {
				if files != 0 {
					return nil, false, invalidFiles("Upload either one ZIP file or at most 500 files, not both.")
				}
				uploaded, err = io.ReadAll(io.LimitReader(part, agentskill.MaxArchiveBytes+1))
				if err != nil {
					return nil, false, err
				}
				if len(uploaded) > agentskill.MaxArchiveBytes {
					return nil, false, invalidFiles("The ZIP file exceeds 5 MiB.")
				}
				zipUpload = true
			} else {
				if !utf8.ValidString(name) || len(name) > 4096 || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00\r\n") || !strings.Contains(name, "/") || paths[name] {
					return nil, false, invalidFiles("Each file name must be a unique, clean relative path inside a top-level folder.")
				}
				paths[name] = true
				body, err := io.ReadAll(io.LimitReader(part, int64(agentskill.MaxExpandedBytes-expanded)+1))
				if err != nil {
					return nil, false, err
				}
				expanded += len(body)
				if expanded > agentskill.MaxExpandedBytes {
					return nil, false, invalidFiles("The files exceed 20 MiB expanded.")
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
		return nil, false, missingParameter("files")
	}
	if !zipUpload {
		uploaded = archive.Bytes()
	}
	metadata, err := agentskill.Inspect(uploaded)
	if err != nil {
		return nil, false, invalidFiles("The bundle must hold one top-level folder with a valid SKILL.md and files within the limits.")
	}
	// Installed and plugin Skills keep the wider native rules; only this route is stricter.
	if strings.Contains(metadata.Name, "_") {
		return nil, false, invalidFiles("The Skill name must not contain '_'.")
	}
	if utf8.RuneCountInString(metadata.Description) > 1024 {
		return nil, false, invalidFiles("The Skill description must be at most 1024 characters.")
	}
	return uploaded, makeDefault, nil
}

func invalidFiles(message string) error {
	return &fieldError{param: "files", code: "invalid_value", message: message}
}
