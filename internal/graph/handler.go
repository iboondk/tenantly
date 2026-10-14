package graph

import (
	"net/http"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/iboondk/tenantly/internal/graph/generated"
)

// maxComplexity caps the cost of one query so a deep or wide query is rejected
// before it runs.
const maxComplexity = 200

// NewHandler returns the GraphQL endpoint. Only POST is accepted. Introspection
// is served only when introspection is true. The caller sets it to false in prod.
func NewHandler(r *Resolver, introspection bool) http.Handler {
	srv := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: r}))
	srv.AddTransport(transport.POST{})
	srv.Use(extension.FixedComplexityLimit(maxComplexity))
	if introspection {
		srv.Use(extension.Introspection{})
	}
	return srv
}
