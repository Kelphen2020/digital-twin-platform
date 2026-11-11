package store_test

import (
	"context"
	"os"
	"testing"

	"digital-twin-platform/api/internal/store"
)

// TestPostgresRateStore exercises the shared token bucket (P31.0). Skipped
// unless PG_TEST_URL points at a disposable database.
func TestPostgresRateStore(t *testing.T) {
	url := os.Getenv("PG_TEST_URL")
	if url == "" {
		t.Skip("PG_TEST_URL not set; skipping PostgreSQL rate store test")
	}
	ctx := context.Background()
	pg, err := store.NewPostgres(ctx, url)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pg.Close()

	store := store.NewPostgresRateStore(pg)
	// A bucket of 2 tokens per second: two takas pass, the third is rejected.
	if ok, err := store.Take(ctx, "tnt_rate_it", 2, 2); err != nil || !ok {
		t.Fatalf("first take: %v %v", ok, err)
	}
	if ok, err := store.Take(ctx, "tnt_rate_it", 2, 2); err != nil || !ok {
		t.Fatalf("second take: %v %v", ok, err)
	}
	if ok, err := store.Take(ctx, "tnt_rate_it", 2, 2); err != nil || ok {
		t.Fatalf("third take should be limited: %v %v", ok, err)
	}
	// Unlimited tenants always pass.
	if ok, err := store.Take(ctx, "tnt_rate_it", 0, 0); err != nil || !ok {
		t.Fatalf("unlimited take: %v %v", ok, err)
	}
}
