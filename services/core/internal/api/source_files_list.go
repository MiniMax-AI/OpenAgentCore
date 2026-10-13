package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
)

func (h *Handler) listSourceFiles(w http.ResponseWriter, r *http.Request) {
	options, purpose, ok := readSourceFilePage(w, r)
	if !ok {
		return
	}
	page, err := h.FilesReader.List(r.Context(), tenantID(r), files.ListQuery{After: options.after, Limit: options.limit, Ascending: options.ascending, Purpose: purpose})
	if err != nil {
		writeFilesError(w, r, err, "after")
		return
	}
	response := v1.SourceFileList{Object: "list", Data: make([]v1.SourceFile, 0, len(page.Files)), HasMore: page.NextCursor != ""}
	for _, file := range page.Files {
		response.Data = append(response.Data, sourceFileResponse(file))
	}
	if len(response.Data) > 0 {
		response.FirstID = &response.Data[0].ID
		response.LastID = &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}

func readSourceFilePage(w http.ResponseWriter, r *http.Request) (pageOptions, *string, bool) {
	q := r.URL.Query()
	options, ok := readPageQueryLimits(w, r, q, files.MaxPageSize, files.MaxPageSize, false, "purpose")
	if !ok {
		return pageOptions{}, nil, false
	}
	// An explicit empty purpose applies no filter, as observed on the hosted service.
	values := q["purpose"]
	if len(values) == 0 || values[0] == "" {
		return options, nil, true
	}
	switch values[0] {
	case files.PurposeUserData, "assistants", "batch", "fine-tune", "vision", "evals", "assistants_output", "batch_output", "fine-tune-results":
	default:
		writeError(w, http.StatusBadRequest, "", "Invalid purpose.", "purpose")
		return pageOptions{}, nil, false
	}
	return options, &values[0], true
}
