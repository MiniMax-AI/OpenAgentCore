package api

import "net/http"

// @Summary List source files in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Files
// @Produce json
// @Security DeploymentAdminAuth
// @Param after query string false "Last File ID from the previous page"
// @Param limit query integer false "Maximum page size, 1–10000" default(10000) minimum(1) maximum(10000)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Param purpose query string false "Only return Files with this purpose"
// @Success 200 {object} v1.SourceFileList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/files [get]
func (h *Handler) adminListSourceFiles(w http.ResponseWriter, r *http.Request) {
	h.listSourceFiles(w, r)
}

// @Summary Retrieve source file metadata in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Files
// @Produce json
// @Security DeploymentAdminAuth
// @Param file_id path string true "Source file ID"
// @Success 200 {object} v1.SourceFile
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/files/{file_id} [get]
func (h *Handler) adminGetSourceFile(w http.ResponseWriter, r *http.Request) {
	h.getSourceFile(w, r)
}

// @Summary Delete a source file in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Files
// @Produce json
// @Security DeploymentAdminAuth
// @Param file_id path string true "Source file ID"
// @Success 200 {object} v1.SourceFileDeleted
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/files/{file_id} [delete]
func (h *Handler) adminDeleteSourceFile(w http.ResponseWriter, r *http.Request) {
	h.deleteSourceFile(w, r)
}
