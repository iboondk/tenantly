package graph

import (
	"context"
	"errors"
	"log"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/iboondk/tenantly/internal/auth"
	"github.com/iboondk/tenantly/internal/authz"
	"github.com/iboondk/tenantly/internal/graph/model"
	"github.com/iboondk/tenantly/internal/service"
	"github.com/iboondk/tenantly/internal/store"
)

// gqlError builds an error with a machine-readable code in extensions.code.
func gqlError(code, msg string) *gqlerror.Error {
	return &gqlerror.Error{
		Message:    msg,
		Extensions: map[string]any{"code": code},
	}
}

// toGQLError maps a domain error to a GraphQL error. Non-member and missing
// both become NOT_FOUND, so tenant IDs cannot be enumerated. Unknown errors
// become INTERNAL with a generic message. The detail goes to the log only.
func toGQLError(_ context.Context, err error) error {
	switch {
	case errors.Is(err, authz.ErrNotMember), errors.Is(err, store.ErrNotFound):
		return gqlError("NOT_FOUND", "not found")
	case errors.Is(err, authz.ErrForbidden):
		return gqlError("FORBIDDEN", "forbidden")
	case errors.Is(err, store.ErrConflict):
		return gqlError("CONFLICT", "conflict")
	case errors.Is(err, service.ErrInvalid):
		return gqlError("BAD_USER_INPUT", err.Error())
	default:
		log.Printf("graph: internal error: %v", err)
		return gqlError("INTERNAL", "internal error")
	}
}

// unauthenticatedError is returned when a resolver runs without claims. The
// router's Authenticate middleware normally prevents this.
func unauthenticatedError() error {
	return gqlError("UNAUTHENTICATED", "missing claims")
}

// callerID returns the user ID from the claims in ctx.
func callerID(ctx context.Context) (uuid.UUID, error) {
	claims, ok := auth.FromContext(ctx)
	if !ok {
		return uuid.Nil, unauthenticatedError()
	}
	return claims.UserID, nil
}

func toProject(p store.Project) *model.Project {
	return &model.Project{
		ID:        p.ID,
		TenantID:  p.TenantID,
		Name:      p.Name,
		CreatedAt: p.CreatedAt,
	}
}
