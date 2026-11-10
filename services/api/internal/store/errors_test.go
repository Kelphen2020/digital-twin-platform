package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

// The canonical sentinels must be shared across model, store and auth so that
// errors.Is comparisons work regardless of the implementation.
func TestSentinelIdentity(t *testing.T) {
	if !errors.Is(store.ErrNotFound, model.ErrNotFound) {
		t.Fatal("store.ErrNotFound must alias model.ErrNotFound")
	}
	if !errors.Is(store.ErrConflict, model.ErrConflict) {
		t.Fatal("store.ErrConflict must alias model.ErrConflict")
	}
	wrapped := fmt.Errorf("get camera: %w", store.ErrNotFound)
	if !store.IsNotFound(wrapped) {
		t.Fatal("IsNotFound should unwrap wrapped errors")
	}
	if store.IsNotFound(errors.New("other")) || store.IsNotFound(nil) {
		t.Fatal("IsNotFound should be false for unrelated errors and nil")
	}
	if !store.IsConflict(fmt.Errorf("create user: %w", model.ErrConflict)) {
		t.Fatal("IsConflict should unwrap wrapped conflicts")
	}
}

// Memory and PostgreSQL implementations must agree on the sentinel they return.
func TestMemoryReturnsCanonicalSentinels(t *testing.T) {
	memory := newMemory(t)
	ctx := context.Background()
	if _, err := memory.GetCamera(ctx, "cam_missing"); !store.IsNotFound(err) {
		t.Fatalf("GetCamera: %v", err)
	}
	if _, err := memory.GetOrder(ctx, "ord_missing"); !store.IsNotFound(err) {
		t.Fatalf("GetOrder: %v", err)
	}
	if err := memory.CreateUserAccount(ctx, model.UserAccount{ID: "u1", Username: "dup"}); err != nil {
		t.Fatalf("first user: %v", err)
	}
	if err := memory.CreateUserAccount(ctx, model.UserAccount{ID: "u2", Username: "dup"}); !store.IsConflict(err) {
		t.Fatalf("duplicate user should be conflict, got %v", err)
	}
}
