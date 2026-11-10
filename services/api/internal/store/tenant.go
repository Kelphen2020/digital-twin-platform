package store

import (
	"context"
	"os"

	"digital-twin-platform/api/internal/model"
)

// tenantCtxKey is the private context key carrying the resolved tenant id.
type tenantCtxKey struct{}

// WithTenant returns a context scoped to one tenant. Handlers resolve the
// tenant from the X-Tenant-ID header (or the login body) and attach it before
// touching the store; every tenant-scoped store method reads it from here.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	if tenantID == "" {
		tenantID = model.DefaultTenantID
	}
	return context.WithValue(ctx, tenantCtxKey{}, tenantID)
}

// TenantFrom returns the tenant carried by ctx, defaulting to "default".
func TenantFrom(ctx context.Context) string {
	if ctx == nil {
		return model.DefaultTenantID
	}
	if tenantID, ok := ctx.Value(tenantCtxKey{}).(string); ok && tenantID != "" {
		return tenantID
	}
	return model.DefaultTenantID
}

// TenantShared reports whether TENANT_SHARED=1 is set: all tenants then read
// and write the "default" tenant's data (single-tenant compatibility mode).
func TenantShared() bool { return os.Getenv("TENANT_SHARED") == "1" }

// tenantOf resolves the effective data tenant for a store operation.
func tenantOf(ctx context.Context) string {
	if TenantShared() {
		return model.DefaultTenantID
	}
	return TenantFrom(ctx)
}
