package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/auth"
	"github.com/iboondk/tenantly/internal/service"
)

// createTenant makes the caller the owner of a new tenant.

func (h *handlers) createTenant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	name, err := service.ValidateName(body.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	t, err := h.d.Store.CreateTenant(r.Context(), claims.UserID, name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}{ID: t.ID, Name: t.Name})
}
