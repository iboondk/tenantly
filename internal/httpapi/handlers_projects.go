package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/auth"
	"github.com/iboondk/tenantly/internal/store"
)

const defaultListLimit = 50

// callerFromRequest returns the caller ID and membership set by the middleware.

func callerFromRequest(r *http.Request) (uuid.UUID, store.Membership, bool) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		return uuid.Nil, store.Membership{}, false
	}
	m, ok := membershipFromContext(r.Context())
	return claims.UserID, m, ok
}

type projectJSON struct {
	ID        uuid.UUID `json:"id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func toProjectJSON(p store.Project) projectJSON {
	return projectJSON{ID: p.ID, TenantID: p.TenantID, Name: p.Name, CreatedAt: p.CreatedAt}
}

// listProjects returns up to ?limit= projects of the tenant (default 50).

func (h *handlers) listProjects(w http.ResponseWriter, r *http.Request) {
	userID, m, ok := callerFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	limit := defaultListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "limit must be an integer")
			return
		}
		limit = n
	}
	list, err := h.d.Projects.List(r.Context(), m.TenantID, userID, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]projectJSON, 0, len(list))
	for _, p := range list {
		out = append(out, toProjectJSON(p))
	}
	writeJSON(w, http.StatusOK, map[string][]projectJSON{"projects": out})
}

// createProject adds a project to the tenant. The route requires admin.

func (h *handlers) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	userID, m, ok := callerFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	p, err := h.d.Projects.Create(r.Context(), m.TenantID, userID, body.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toProjectJSON(p))
}

// getProject returns one project. A project of another tenant is a 404.

func (h *handlers) getProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	userID, m, ok := callerFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	p, err := h.d.Projects.Get(r.Context(), m.TenantID, userID, projectID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectJSON(p))
}
