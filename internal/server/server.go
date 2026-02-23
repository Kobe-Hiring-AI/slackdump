package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/rusq/slackdump/v4/internal/server/api"
	"github.com/rusq/slackdump/v4/internal/server/auth"
	"github.com/rusq/slackdump/v4/internal/server/engine"
	"github.com/rusq/slackdump/v4/internal/server/storage"
	"github.com/rusq/slackdump/v4/internal/server/store"
)

// ExportEngine is the interface the server needs from the engine.
type ExportEngine interface {
	SubmitExport(ctx context.Context, job *store.ExportJob)
}

// Server is the HTTP server for the multi-tenant Slackdump API.
type Server struct {
	cfg         Config
	store       *store.Store
	engine      ExportEngine
	validator   api.CredentialValidator
	tokenIssuer *auth.TokenIssuer
	sqldClient  *engine.SqldClient
	storage     storage.Storage
	srv         *http.Server
}

// NewServer creates and configures a new Server.
// If issuer is nil and cfg.JWTPrivateKeyPath is set, NewServer will load the key
// and create a TokenIssuer automatically. Pass a non-nil issuer to override.
func NewServer(cfg Config, s *store.Store, eng ExportEngine, issuer *auth.TokenIssuer, st storage.Storage) *Server {
	var sqld *engine.SqldClient
	if cfg.SqldAdminURL != "" {
		sqld = engine.NewSqldClient(cfg.SqldAdminURL)
	}
	return newServer(cfg, s, eng, &slackValidator{}, issuer, sqld, st)
}

func newServer(cfg Config, s *store.Store, eng ExportEngine, v api.CredentialValidator, issuer *auth.TokenIssuer, sqld *engine.SqldClient, st storage.Storage) *Server {
	sv := &Server{
		cfg:         cfg,
		store:       s,
		engine:      eng,
		validator:   v,
		tokenIssuer: issuer,
		sqldClient:  sqld,
		storage:     st,
	}

	r := sv.routes()
	sv.srv = &http.Server{
		Addr:    cfg.Addr,
		Handler: r,
	}
	return sv
}

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.Logger, middleware.Recoverer)

	// Public routes (no auth required).
	r.Get("/openapi.yaml", SwaggerSpecHandler())
	r.Get("/docs", SwaggerUIHandler())

	// Authenticated API routes.
	r.Group(func(r chi.Router) {
		r.Use(JSONMiddleware)
		r.Use(AuthMiddleware(s.store, s.cfg.AdminKey))

		tenantH := api.NewTenantHandler(s.store, s.cfg.EncryptionKey, s.validator, s.engine, s.sqldClient)
		apikeyH := api.NewAPIKeyHandler(s.store)
		exportH := api.NewExportHandler(s.store, s.engine)
		downloadH := api.NewDownloadHandler(s.cfg.DataDir, s.store, s.storage)
		connectH := api.NewConnectHandler(s.tokenIssuer, s.cfg.SqldPublicURL)

		r.Route("/tenants", func(r chi.Router) {
			r.With(AdminOnly(s.cfg.AdminKey)).Post("/", tenantH.Create)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", tenantH.Get)
				r.With(AdminOnly(s.cfg.AdminKey)).Delete("/", tenantH.Delete)
				r.Post("/keys", apikeyH.Create)
				r.Delete("/keys/{key_id}", apikeyH.Revoke)
				r.Post("/exports", exportH.Create)
				r.Get("/exports", exportH.List)
				r.Get("/exports/{job_id}", exportH.Get)
				r.Get("/export/download", downloadH.Download)
				r.Get("/connect", connectH.Connect)
				r.Get("/status", connectH.Status)
			})
		})
	})

	return r
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
