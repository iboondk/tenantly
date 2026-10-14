// Package httpapi serves the Tenantly REST API.
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/iboondk/tenantly/internal/authz"
	"github.com/iboondk/tenantly/internal/service"
	"github.com/iboondk/tenantly/internal/store"
)

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeError writes the JSON error envelope {error, message}.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: code, Message: msg})
}

// writeErr maps a domain error to a status and code. Unknown errors become a
// 500 whose message is generic. The detail goes to the server log only.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, authz.ErrNotMember), errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, authz.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "forbidden")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, service.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	default:
		log.Printf("httpapi: internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}
