package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

// @Summary List source files
// @Description Lists project-owned Files without reading their bodies. The limit defaults to 10000 and must be 1–10000. Equal creation times use ID ordering. Purpose validation precedes cursor lookup; current storage contains only user_data. An explicit empty purpose is treated as omitted. Repeated purpose values remain rejected. Hosted positive filtering, default order and concurrent-page behavior remain unverified. No Beta header is required.
// @Tags Files
// @Produce json
// @Security BearerAuth
// @Param after query string false "Last File ID from the previous page"
// @Param limit query integer false "Maximum page size, 1–10000" default(10000) minimum(1) maximum(10000)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Param purpose query string false "Only return Files with this purpose"
// @Success 200 {object} v1.SourceFileList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /files [get]
func (h *Handler) listSourceFiles(w http.ResponseWriter, r *http.Request) {
	if !h.sourceFilesAvailable(w) {
		return
	}
	options, purpose, ok := readSourceFilePage(w, r)
	if !ok {
		return
	}
	page, err := h.sourceFiles.ListSourceFiles(r.Context(), tenantID(r), options.after, options.limit, options.ascending, purpose)
	if err != nil {
		writeStoreError(w, r, err, "after")
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
	options, ok := readPageQueryLimits(w, r, q, 10000, 10000, false, "purpose")
	if !ok {
		return pageOptions{}, nil, false
	}
	// An explicit empty purpose applies no filter, as observed on the hosted service.
	values := q["purpose"]
	if len(values) == 0 || values[0] == "" {
		return options, nil, true
	}
	switch values[0] {
	case "user_data", "assistants", "batch", "fine-tune", "vision", "evals", "assistants_output", "batch_output", "fine-tune-results":
	default:
		writeError(w, http.StatusBadRequest, "", "Invalid purpose.", "purpose")
		return pageOptions{}, nil, false
	}
	return options, &values[0], true
}
