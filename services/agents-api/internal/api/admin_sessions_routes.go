package api

import "net/http"

// @Summary List execution Sessions in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Sessions
// @Produce json
// @Security DeploymentAdminAuth
// @Param agent_id query string false "Root Agent ID whose Sessions to return"
// @Param after query string false "Last Session ID from the previous page"
// @Param limit query int false "Page size; 0 is treated as 1 and values above 100 as 100" minimum(0) default(20)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.SessionList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions [get]
func (h *Handler) adminListSessions(w http.ResponseWriter, r *http.Request) {
	h.listSessions(w, r)
}

// @Summary Retrieve an execution Session in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Sessions
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.Session
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id} [get]
func (h *Handler) adminGetSession(w http.ResponseWriter, r *http.Request) {
	h.getSession(w, r)
}

// @Summary Delete an execution Session in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Sessions
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.SessionDeleted
// @Failure 400,401,404,409,413,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id} [delete]
func (h *Handler) adminDeleteSession(w http.ResponseWriter, r *http.Request) {
	h.deleteSession(w, r)
}

// @Summary List execution Turns in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Turns
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Param after query string false "Last Turn ID from the previous page"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.TurnList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/turns [get]
func (h *Handler) adminListTurns(w http.ResponseWriter, r *http.Request) {
	h.listTurns(w, r)
}

// @Summary Retrieve an execution Turn in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Turns
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Param turn_id path string true "Turn ID"
// @Success 200 {object} v1.Turn
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/turns/{turn_id} [get]
func (h *Handler) adminGetTurn(w http.ResponseWriter, r *http.Request) {
	h.getTurn(w, r)
}

// @Summary List persisted execution Items in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Items
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Param after query string false "Last Item ID from the previous page"
// @Param limit query int false "Page size; 0 is treated as 1 and values above 100 as 100" minimum(0) default(20)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.ItemList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/items [get]
func (h *Handler) adminListItems(w http.ResponseWriter, r *http.Request) {
	h.listItems(w, r)
}

// @Summary List immutable Session artifacts in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Artifacts
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Param environment_id query string false "Producing Environment ID; an unknown or malformed ID returns an empty page"
// @Param after query string false "Last immutable artifact ID"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Publication order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.SessionArtifactList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/artifacts [get]
func (h *Handler) adminListSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	h.listSessionArtifacts(w, r)
}

// @Summary Retrieve immutable artifact metadata in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Artifacts
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Param artifact_id path string true "Artifact ID"
// @Success 200 {object} v1.SessionArtifact
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/artifacts/{artifact_id} [get]
func (h *Handler) adminGetSessionArtifact(w http.ResponseWriter, r *http.Request) {
	h.getSessionArtifact(w, r)
}

// @Summary Delete a published artifact in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Artifacts
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Param artifact_id path string true "Artifact ID"
// @Success 200 {object} v1.SessionArtifactDeleted
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/artifacts/{artifact_id} [delete]
func (h *Handler) adminDeleteSessionArtifact(w http.ResponseWriter, r *http.Request) {
	h.deleteSessionArtifact(w, r)
}

// @Summary Download immutable artifact bytes in a Project
// @Description Core key only. Reuses the public resource projection and operation rules; the Project ID selects the target space and does not authenticate.
// @Tags Artifacts
// @Produce octet-stream
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Param artifact_id path string true "Artifact ID"
// @Success 200 {file} binary
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/artifacts/{artifact_id}/content [get]
func (h *Handler) adminSessionArtifactContent(w http.ResponseWriter, r *http.Request) {
	h.sessionArtifactContent(w, r)
}

// @Summary Retrieve a Session's frozen execution configuration in a Project
// @Description Core key only; the Project ID selects the target space and does not authenticate. Returns the committed model, harness and safe provider selection with recorded sources. This read never decrypts credentials, resolves current defaults or probes execution health. Deployment defaults frozen after they moved into Core show their safe view; older deployment selections remain redacted. Historical provenance and missing provider projections are explicitly unknown/unavailable.
// @Tags Execution configuration
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.SessionExecutionConfiguration
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/execution-configuration [get]
func (h *Handler) adminGetSessionExecutionConfiguration(w http.ResponseWriter, r *http.Request) {
	h.getSessionExecutionConfiguration(w, r)
}

// @Summary Retrieve a Session Runtime observation in a Project
// @Description Core key only; the Project ID selects the target space and does not authenticate. Returns one read-only current Runtime observation. It never provisions, renews, restarts, pauses or stops compute.
// @Tags Runtime observations
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.RuntimeObservation
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/runtime-observation [get]
func (h *Handler) adminGetRuntimeObservation(w http.ResponseWriter, r *http.Request) {
	h.getRuntimeObservation(w, r)
}

// @Summary Retrieve Session Runtime history in a Project
// @Description Core key only; the Project ID selects the target space and does not authenticate. Returns stored Runtime observations for one Session. End is exclusive; the server selects a bounded resolution. Responses contain at most 1,000 series, 10,000 points per coverage/series array, and 100,000 total coverage plus series points. It never reads or changes live compute.
// @Tags Runtime history
// @Produce json
// @Security DeploymentAdminAuth
// @Param session_id path string true "Session ID"
// @Param start query integer true "Inclusive Unix-second start" minimum(0) maximum(9007199254740991)
// @Param end query integer true "Exclusive Unix-second end" minimum(1) maximum(9007199254740991)
// @Param max_points query integer false "Maximum points per series; defaults to the lower of 120 and the advertised service maximum" minimum(2) maximum(10000)
// @Success 200 {object} v1.RuntimeHistory
// @Failure 400,401,404,409,503 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/projects/{project_id}/sessions/{session_id}/runtime-history [get]
func (h *Handler) adminGetRuntimeHistory(w http.ResponseWriter, r *http.Request) {
	h.getRuntimeHistory(w, r)
}
