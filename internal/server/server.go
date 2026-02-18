package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/rusq/slackdump/v4/internal/server/api"
	"github.com/rusq/slackdump/v4/internal/server/store"
)

// ExportEngine is the interface the server needs from the engine.
type ExportEngine interface {
	SubmitExport(ctx context.Context, job *store.ExportJob)
}

// Server is the HTTP server for the multi-tenant Slackdump API.
type Server struct {
	cfg       Config
	store     *store.Store
	engine    ExportEngine
	validator api.CredentialValidator
	srv       *http.Server
}

// NewServer creates and configures a new Server.
func NewServer(cfg Config, s *store.Store, engine ExportEngine) *Server {
	return newServer(cfg, s, engine, &slackValidator{})
}

func newServer(cfg Config, s *store.Store, engine ExportEngine, v api.CredentialValidator) *Server {
	sv := &Server{
		cfg:       cfg,
		store:     s,
		engine:    engine,
		validator: v,
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

		tenantH := api.NewTenantHandler(s.store, s.cfg.EncryptionKey, s.validator, s.engine)
		apikeyH := api.NewAPIKeyHandler(s.store)
		exportH := api.NewExportHandler(s.store, s.engine)
		downloadH := api.NewDownloadHandler(s.cfg.DataDir)

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
