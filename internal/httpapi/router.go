package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/iboondk/tenantly/internal/auth"
	"github.com/iboondk/tenantly/internal/authz"
	"github.com/iboondk/tenantly/internal/config"
	"github.com/iboondk/tenantly/internal/service"
	"github.com/iboondk/tenantly/internal/store"
)

const serviceName = "tenantly-api"

// Deps holds what the router needs. Middlewares run after Authenticate on
// every authenticated route, for example a rate limiter. GraphQL is optional.
type Deps struct {
	Cfg         config.Config
	Issuer      *auth.Issuer
	OAuth       *auth.OAuth
	Store       *store.Store
	Authz       *authz.Authorizer
	Projects    service.Projects
	Middlewares []func(http.Handler) http.Handler
	GraphQL     http.Handler
}

// NewRouter builds the full HTTP surface, wrapped in otelhttp.
func NewRouter(d Deps) http.Handler {
	h := &handlers{d: d}
	r := chi.NewRouter()

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/v1", func(v chi.Router) {
		v.Route("/auth", func(a chi.Router) {
			if isDevEnv(d.Cfg.AppEnv) {
				a.Post("/dev-token", h.devToken)
			}
			if d.OAuth != nil {
				a.Get("/oauth/login", h.oauthLogin)
				a.Get("/oauth/callback", h.oauthCallback)
			}
		})

		v.Group(func(g chi.Router) {
			g.Use(Authenticate(d.Issuer))
			g.Use(d.Middlewares...)

			g.Post("/tenants", h.createTenant)

			g.Route("/tenants/{tenantID}", func(t chi.Router) {
				t.With(RequireMembership(d.Authz, authz.RoleMember)).
					Get("/projects", h.listProjects)
				t.With(RequireMembership(d.Authz, authz.RoleAdmin)).
					Post("/projects", h.createProject)
				t.With(RequireMembership(d.Authz, authz.RoleMember)).
					Get("/projects/{projectID}", h.getProject)
			})
		})
	})

	if d.GraphQL != nil {
		r.Group(func(gq chi.Router) {
			gq.Use(Authenticate(d.Issuer))
			gq.Use(d.Middlewares...)
			gq.Handle("/graphql", d.GraphQL)
		})
	}

	return otelhttp.NewHandler(r, serviceName)
}

// isDevEnv reports whether the dev-token route may be served.
func isDevEnv(env string) bool {
	return env == "dev" || env == "test"
}
