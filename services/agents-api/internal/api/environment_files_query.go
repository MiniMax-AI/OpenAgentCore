package api

import (
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type environmentFileOptions struct {
	directory         string
	relativeDirectory string
	limit             int
	ascending         bool
	binding           string
	cursor            *environmentFileCursor
}

var environmentFileQueryKeys = []string{"path", "limit", "order", "page"}

// Official Files.list path errors (HE-38, HE-39).
var (
	errEnvironmentFileDirectory = &fieldError{message: "path must be an absolute directory inside /workspace"}
	errEnvironmentFileCanonical = &fieldError{message: "path must identify a non-reserved directory inside /workspace"}
)

// readEnvironmentFileQuery follows the shared list rules: unknown keys are
// ignored and a repeated supported key uses the Beta duplicate-field error.
// Unlike the shared lists, which drop malformed pairs, it rejects malformed
// query encoding.
func readEnvironmentFileQuery(w http.ResponseWriter, r *http.Request, environment store.Environment) (environmentFileOptions, bool) {
	var options environmentFileOptions
	// Malformed query encoding remains a local rejection; no official sample exists.
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return options, false
	}
	for _, key := range environmentFileQueryKeys {
		if len(q[key]) > 1 {
			writeListDuplicateError(w, r, key, environmentFileQueryKeys)
			return options, false
		}
	}
	pageQuery := url.Values{}
	for _, key := range []string{"limit", "order"} {
		if values, exists := q[key]; exists {
			pageQuery[key] = values
		}
	}
	page, ok := readPageQuery(w, r, pageQuery, false)
	if !ok {
		return options, false
	}
	const root = "/workspace"
	directory := root
	if requested, exists := q["path"]; exists {
		if err := environmentFileDirectoryError(requested[0]); err != nil {
			writeFieldError(w, err)
			return options, false
		}
		directory = requested[0]
	}
	options = environmentFileOptions{directory: directory, limit: page.limit, ascending: page.ascending}
	if directory != root {
		options.relativeDirectory = strings.TrimPrefix(directory, root+"/")
	}
	options.binding = environmentFilesDigest([]any{tenantID(r), environment.ID, directory, options.limit, options.ascending})
	if token, exists := q["page"]; exists {
		cursor, err := decodeEnvironmentFileCursor(token[0], options.binding)
		if err != nil {
			writeFieldError(w, err)
			return environmentFileOptions{}, false
		}
		options.cursor = &cursor
	}
	return options, true
}

// environmentFileDirectoryError accepts only the cleaned form of the workspace
// root or a directory below it. Nothing is normalized before execution.
func environmentFileDirectoryError(value string) error {
	switch {
	case !path.IsAbs(value) || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00\r\n"):
		return errEnvironmentFileDirectory
	case path.Clean(value) != value:
		return errEnvironmentFileCanonical
	case value != "/workspace" && !strings.HasPrefix(value, "/workspace/"):
		return errEnvironmentFileDirectory
	}
	return nil
}
