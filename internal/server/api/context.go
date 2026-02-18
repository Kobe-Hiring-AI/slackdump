package api

import "context"

type contextKey string

const (
	TenantIDKey contextKey = "tenant_id"
	IsAdminKey  contextKey = "is_admin"
)

// TenantFromContext returns the tenant ID from the request context.
func TenantFromContext(ctx context.Context) string {
	v, _ := ctx.Value(TenantIDKey).(string)
	return v
}

// IsAdmin returns true if the request was authenticated with the admin key.
func IsAdmin(ctx context.Context) bool {
	v, _ := ctx.Value(IsAdminKey).(bool)
	return v
}
