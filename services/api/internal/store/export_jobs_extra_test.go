package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

// P34: cover the remaining export job branches (keep-based purge, empty lists).
func TestExportJobExtraBranches(t *testing.T) {
	memory := store.NewMemory(2000)
	ctx := context.Background()
	now := time.Now().UTC()

	// Empty store: list returns no rows, delete is not found, purge returns none.
	if items, err := memory.ListExportJobs(ctx, "default", 10); err != nil || len(items) != 0 {
		t.Fatalf("empty list: %v %v", items, err)
	}
	if err := memory.DeleteExportJob(ctx, "exp_missing"); !store.IsNotFound(err) {
		t.Fatalf("delete missing: %v", err)
	}
	if removed, err := memory.PurgeExportJobs(ctx, "default", now, 0); err != nil || len(removed) != 0 {
		t.Fatalf("purge empty: %v %v", removed, err)
	}
	if err := memory.UpdateExportJob(ctx, model.ExportJob{ID: "exp_missing"}); !store.IsNotFound(err) {
		t.Fatalf("update missing: %v", err)
	}

	// Ten finished jobs, keep only 3 → seven purged.
	for i := 0; i < 10; i++ {
		job := model.ExportJob{
			ID: fmt.Sprintf("exp_keep_%02d", i), TenantID: "default", Status: model.ExportDone,
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		}
		if err := memory.CreateExportJob(ctx, job); err != nil {
			t.Fatalf("create job %d: %v", i, err)
		}
	}
	removed, err := memory.PurgeExportJobs(ctx, "default", now.Add(-24*time.Hour), 3)
	if err != nil || len(removed) != 7 {
		t.Fatalf("keep-based purge: %v %v", removed, err)
	}
	remaining, err := memory.ListExportJobs(ctx, "default", 50)
	if err != nil || len(remaining) != 3 {
		t.Fatalf("remaining jobs: %d %v", len(remaining), err)
	}
	// Newest first ordering.
	if remaining[0].ID != "exp_keep_09" {
		t.Fatalf("unexpected order: %+v", remaining[0])
	}

	// List limit truncates.
	if items, err := memory.ListExportJobs(ctx, "default", 2); err != nil || len(items) != 2 {
		t.Fatalf("limit: %d %v", len(items), err)
	}
}
