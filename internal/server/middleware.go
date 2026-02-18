package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/rusq/slackdump/v4/internal/server/api"
	"github.com/rusq/slackdump/v4/internal/server/store"
)

// TenantFromContext returns the tenant ID from the request context.
func TenantFromContext(ctx context.Context) string {
	return api.TenantFromContext(ctx)
}

// JSONMiddleware sets Content-Type: application/json on responses.
func JSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// AuthMiddleware validates Bearer token against the store.
// It extracts the token from Authorization header, hashes it with store.HashKey,
// looks it up via store.APIKeys.LookupByHash. If found, sets tenant_id in context.
// Also checks if the raw key matches adminKey for admin endpoints.
func AuthMiddleware(s *store.Store, adminKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r)
			if token == "" {
				respondError(w, http.StatusUnauthorized, "missing or invalid authorization header")
				return
			}

			// Check admin key first.
			if adminKey != "" && token == adminKey {
				ctx := context.WithValue(r.Context(), api.IsAdminKey, true)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Look up tenant API key.
			hash := store.HashKey(token)
			ak, err := s.APIKeys.LookupByHash(r.Context(), hash)
			if err != nil {
				respondError(w, http.StatusUnauthorized, "invalid api key")
				return
			}

			ctx := context.WithValue(r.Context(), api.TenantIDKey, ak.TenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AdminOnly middleware checks that the request was authenticated with the admin key.
func AdminOnly(adminKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !api.IsAdmin(r.Context()) {
				respondError(w, http.StatusForbidden, "admin access required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimPrefix(auth, prefix)
}
