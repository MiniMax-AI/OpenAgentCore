package api

import (
	"context"
	"io"
	"net/http"
	"path"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/go-chi/chi/v5"
)

// Artifacts deletes a Session's published Artifacts.
type Artifacts interface {
	DeleteSessionArtifact(context.Context, sessions.DeleteSessionArtifactCommand) error
}

// ArtifactsReader reads and streams a Session's published Artifacts.
type ArtifactsReader interface {
	GetSessionArtifact(context.Context, string, string, string) (sessions.Artifact, error)
	ListSessionArtifacts(context.Context, string, string, string, string, int, bool) (sessions.ArtifactPage, error)
	ReadSessionArtifact(context.Context, string, string, string, func(sessions.Artifact, io.Reader) error) error
}

func (h *Handler) listSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r, "environment_id")
	if !ok {
		return
	}
	page, err := h.ArtifactsReader.ListSessionArtifacts(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), r.URL.Query().Get("environment_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	data := make([]v1.SessionArtifact, 0, len(page.Artifacts))
	for _, artifact := range page.Artifacts {
		data = append(data, artifactResponse(artifact))
	}
	first, last := listBounds(data, func(value v1.SessionArtifact) string { return value.ID })
	writeJSON(w, http.StatusOK, v1.SessionArtifactList{Object: "list", Data: data, HasMore: page.NextCursor != "", FirstID: first, LastID: last})
}

func (h *Handler) getSessionArtifact(w http.ResponseWriter, r *http.Request) {
	artifact, err := h.ArtifactsReader.GetSessionArtifact(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "artifact_id"))
	if err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, artifactResponse(artifact))
}

func (h *Handler) deleteSessionArtifact(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "artifact_id")
	if err := h.Artifacts.DeleteSessionArtifact(r.Context(), sessions.DeleteSessionArtifactCommand{TenantID: tenantID(r), SessionID: chi.URLParam(r, "session_id"), ArtifactID: id}); err != nil {
		writeSessionsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.SessionArtifactDeleted{ID: id, Object: "agent.session.artifact.deleted", Deleted: true})
}

func (h *Handler) sessionArtifactContent(w http.ResponseWriter, r *http.Request) {
	err := serveStoredContent(w, r, func(ctx context.Context, consume func(string, int64, io.Reader) error) error {
		return h.ArtifactsReader.ReadSessionArtifact(ctx, tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "artifact_id"), func(a sessions.Artifact, body io.Reader) error {
			return consume(path.Base(a.Path), a.SizeBytes, body)
		})
	})
	if err != nil {
		writeSessionsError(w, r, err)
	}
}

func artifactResponse(a sessions.Artifact) v1.SessionArtifact {
	return v1.SessionArtifact{ID: a.ID, CreatedAt: a.CreatedAt.Unix(), EnvironmentID: a.EnvironmentID,
		Object: "agent.session.artifact", Path: a.Path, SessionID: a.SessionID, SizeBytes: a.SizeBytes, TurnID: a.TurnID}
}
