// Package service holds the application logic that sits between the HTTP
// layer and the store.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/store"
)

const (
	nameMinRunes = 1
	nameMaxRunes = 200
)

// ErrInvalid marks input that fails validation. The HTTP layer answers 400.
var ErrInvalid = errors.New("invalid input")

// Projects is the project use case. Callers must have passed the membership
// check first; this layer does not repeat it.
type Projects interface {
	List(ctx context.Context, tenantID, userID uuid.UUID, limit int) ([]store.Project, error)
	Get(ctx context.Context, tenantID, userID, projectID uuid.UUID) (store.Project, error)
	Create(ctx context.Context, tenantID, userID uuid.UUID, name string) (store.Project, error)
}

type projects struct {
	s *store.Store
}

// NewProjects wraps the store with the Projects interface.
func NewProjects(s *store.Store) Projects {
	return &projects{s: s}
}

func (p *projects) List(ctx context.Context, tenantID, userID uuid.UUID, limit int) ([]store.Project, error) {
	return p.s.ListProjects(ctx, tenantID, userID, limit)
}

func (p *projects) Get(ctx context.Context, tenantID, userID, projectID uuid.UUID) (store.Project, error) {
	return p.s.GetProject(ctx, tenantID, userID, projectID)
}

func (p *projects) Create(ctx context.Context, tenantID, userID uuid.UUID, name string) (store.Project, error) {
	clean, err := ValidateName(name)
	if err != nil {
		return store.Project{}, err
	}
	return p.s.CreateProject(ctx, tenantID, userID, clean)
}

// ValidateName trims name and checks that it is 1 to 200 characters. The
// error wraps ErrInvalid.
func ValidateName(name string) (string, error) {
	clean := strings.TrimSpace(name)
	n := utf8.RuneCountInString(clean)
	if n < nameMinRunes || n > nameMaxRunes {
		return "", fmt.Errorf("%w: name must be %d to %d characters", ErrInvalid, nameMinRunes, nameMaxRunes)
	}
	return clean, nil
}
