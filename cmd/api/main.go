// Command api serves the Tenantly HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/iboondk/tenantly/internal/auth"
	"github.com/iboondk/tenantly/internal/authz"
	"github.com/iboondk/tenantly/internal/cache"
	"github.com/iboondk/tenantly/internal/config"
	"github.com/iboondk/tenantly/internal/db"
	"github.com/iboondk/tenantly/internal/graph"
	"github.com/iboondk/tenantly/internal/httpapi"
	"github.com/iboondk/tenantly/internal/ratelimit"
	"github.com/iboondk/tenantly/internal/service"
	"github.com/iboondk/tenantly/internal/store"
	"github.com/iboondk/tenantly/internal/telemetry"
)

const serviceName = "tenantly-api"

func main() {
	if err := run(); err != nil {
		log.Fatalf("api: %v", err)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTelemetry, err := telemetry.Setup(ctx, cfg.OTelExporter, cfg.OTLPEndpoint, serviceName)
	if err != nil {
		return err
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(sctx); err != nil {
			log.Printf("api: telemetry shutdown: %v", err)
		}
	}()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(redisOpts)
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Printf("api: redis close: %v", err)
		}
	}()

	st := store.New(pool)
	limiter := ratelimit.New(rdb, cfg.RateLimitPerMinute, time.Minute, time.Now)
	tenantCache := cache.New(rdb, cfg.CacheTTL)

	authorizer := authz.New(st)
	projects := service.NewCachedProjects(service.NewProjects(st), tenantCache)

	// GraphQL shares the authorizer and the cached project service with REST.
	// Introspection is off in prod.
	gql := graph.NewHandler(&graph.Resolver{
		Store:    st,
		Authz:    authorizer,
		Projects: projects,
	}, cfg.AppEnv != "prod")

	router := httpapi.NewRouter(httpapi.Deps{
		Cfg:      cfg,
		Issuer:   auth.NewIssuer(cfg.JWTSecret, cfg.JWTTTL, time.Now),
		OAuth:    auth.NewOAuth(cfg.OAuth, nil),
		Store:    st,
		Authz:    authorizer,
		Projects: projects,
		Middlewares: []func(http.Handler) http.Handler{
			ratelimit.Middleware(limiter, ratelimit.KeyByTenantUser),
		},
		GraphQL: gql,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Printf("api: listening on %s (env=%s)", cfg.HTTPAddr, cfg.AppEnv)

	select {
	case <-ctx.Done():
		log.Printf("api: shutdown signal received")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
