// Package graph is the GraphQL gateway. Resolvers reuse the same
// authorization and service layer as the REST handlers.
package graph

import (
	"github.com/iboondk/tenantly/internal/authz"
	"github.com/iboondk/tenantly/internal/service"
	"github.com/iboondk/tenantly/internal/store"
)

// defaultProjectsLimit is the schema default for projects(limit).
const defaultProjectsLimit = 50

// Resolver is the root of the GraphQL resolvers.
type Resolver struct {
	Store    *store.Store
	Authz    *authz.Authorizer
	Projects service.Projects
}
