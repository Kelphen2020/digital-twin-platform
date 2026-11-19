package store_test

import (
	"context"
	"testing"
	"time"

	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

func TestExportJobPersistenceContract(t *testing.T) {
	memory := store.NewMemory(2000)
	ctx := context.Background()
	now := time.Now().UTC()

	job := model.ExportJob{
		ID: "exp_a", TenantID: "default", Status: model.ExportPending,
		IncludeAudit: true, CreatedBy: "admin", CreatedAt: now, Counts: map[string]int{},
	}
	if err := memory.CreateExportJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := memory.CreateExportJob(ctx, job); !store.IsConflict(err) {
		t.Fatalf("duplicate job id must conflict, got %v", err)
	}

	loaded, ok, err := memory.GetExportJob(ctx, "exp_a")
	if err != nil || !ok || loaded.TenantID != "default" || loaded.Status != model.ExportPending {
		t.Fatalf("get job: %v %v %+v", ok, err, loaded)
	}

	loaded.Status = model.ExportDone
	loaded.SizeBytes = 1234
	loaded.Counts = map[string]int{"cameras": 6}
	if err := memory.UpdateExportJob(ctx, loaded); err != nil {
		t.Fatalf("update job: %v", err)
	}

	// A second tenant's job must not leak into the list.
	if err := memory.CreateExportJob(ctx, model.ExportJob{
		ID: "exp_b", TenantID: "tnt_other", Status: model.ExportDone, CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("create other tenant job: %v", err)
	}
	items, err := memory.ListExportJobs(ctx, "default", 50)
	if err != nil || len(items) != 1 || items[0].ID != "exp_a" {
		t.Fatalf("list jobs: %v %+v", err, items)
	}

	// Restart recovery marks pending jobs failed.
	if err := memory.CreateExportJob(ctx, model.ExportJob{
		ID: "exp_stale", TenantID: "default", Status: model.ExportRunning, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create stale job: %v", err)
	}
	count, err := memory.FailPendingExportJobs(ctx, "interrupted by restart")
	if err != nil || count != 1 {
		t.Fatalf("fail pending jobs: %d %v", count, err)
	}
	stale, _, _ := memory.GetExportJob(ctx, "exp_stale")
	if stale.Status != model.ExportFailed || stale.Error != "interrupted by restart" {
		t.Fatalf("stale job not marked failed: %+v", stale)
	}

	// Purge removes finished jobs older than the cutoff but keeps running ones.
	old := now.Add(-48 * time.Hour)
	if err := memory.CreateExportJob(ctx, model.ExportJob{
		ID: "exp_old", TenantID: "default", Status: model.ExportDone, CreatedAt: old,
	}); err != nil {
		t.Fatalf("create old job: %v", err)
	}
	removed, err := memory.PurgeExportJobs(ctx, "default", now.Add(-24*time.Hour), 0)
	if err != nil || len(removed) != 1 || removed[0] != "exp_old" {
		t.Fatalf("purge: %v %v", removed, err)
	}
	if _, ok, _ := memory.GetExportJob(ctx, "exp_old"); ok {
		t.Fatal("purged job still present")
	}
	if _, ok, _ := memory.GetExportJob(ctx, "exp_a"); !ok {
		t.Fatal("recent finished job must survive")
	}

	if err := memory.DeleteExportJob(ctx, "exp_a"); err != nil {
		t.Fatalf("delete job: %v", err)
	}
	if err := memory.DeleteExportJob(ctx, "exp_a"); !store.IsNotFound(err) {
		t.Fatalf("second delete must be not found, got %v", err)
	}
}

// P38: a job can only be claimed once, so a second replica must lose the race.
func TestExportJobClaimIsExclusive(t *testing.T) {
	memory := store.NewMemory(2000)
	ctx := context.Background()
	now := time.Now().UTC()
	job := model.ExportJob{ID: "exp_claim", TenantID: "default", Status: model.ExportPending, CreatedAt: now}
	if err := memory.CreateExportJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	claimed, err := memory.ClaimExportJob(ctx, "exp_claim", "replica-a")
	if err != nil || !claimed {
		t.Fatalf("first claim should win: %v %v", claimed, err)
	}
	// Second replica loses.
	claimed, err = memory.ClaimExportJob(ctx, "exp_claim", "replica-b")
	if err != nil || claimed {
		t.Fatalf("second claim must lose: %v %v", claimed, err)
	}
	stored, _, _ := memory.GetExportJob(ctx, "exp_claim")
	if stored.Status != model.ExportRunning || stored.Owner != "replica-a" || stored.StartedAt == nil {
		t.Fatalf("claimed job state: %+v", stored)
	}
	// Unknown job surfaces not-found.
	if _, err := memory.ClaimExportJob(ctx, "exp_missing", "x"); !store.IsNotFound(err) {
		t.Fatalf("claim missing job: %v", err)
	}
}

// P42: the histogram buckets events and fills empty slots so charts stay continuous.
func TestEventHistogramBucketing(t *testing.T) {
	memory := store.NewMemory(2000)
	ctx := context.Background()
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	ctx = store.WithTenant(ctx, model.DefaultTenantID)

	base := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
	cameras, err := memory.ListCameras(ctx)
	if err != nil || len(cameras) == 0 {
		t.Fatalf("seed cameras: %v", err)
	}
	camera := cameras[0]
	events := []model.Event{
		{ID: "evt_h1", CameraID: camera.ID, CameraCode: camera.Code, CameraName: camera.Name,
			Type: "intrusion", Severity: "warn", OccurredAt: base.Add(5 * time.Minute), Status: "new"},
		{ID: "evt_h2", CameraID: camera.ID, CameraCode: camera.Code, CameraName: camera.Name,
			Type: "intrusion", Severity: "warn", OccurredAt: base.Add(35 * time.Minute), Status: "new"},
		{ID: "evt_h3", CameraID: camera.ID, CameraCode: camera.Code, CameraName: camera.Name,
			Type: "intrusion", Severity: "warn", OccurredAt: base.Add(2*time.Hour + 10*time.Minute), Status: "new"},
	}
	if err := memory.CreateEvents(ctx, events); err != nil {
		t.Fatalf("create events: %v", err)
	}

	slots, err := memory.EventHistogram(ctx, base, base.Add(4*time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("histogram: %v", err)
	}
	if len(slots) != 5 {
		t.Fatalf("expected 5 hourly slots, got %d", len(slots))
	}
	// base+5m and base+35m both fall in slot 0; base+2h10m falls in slot 2.
	want := []int64{2, 0, 1, 0, 0}
	for i, expected := range want {
		if slots[i].Count != expected {
			t.Fatalf("slot %d: got %d want %d (all=%+v)", i, slots[i].Count, expected, slots)
		}
	}
	// Sub-hour buckets must also work (5 minute slots for the 1h window).
	fine, err := memory.EventHistogram(ctx, base, base.Add(time.Hour), 5*time.Minute)
	if err != nil || len(fine) != 13 {
		t.Fatalf("5m histogram: %d slots err=%v", len(fine), err)
	}
	// base+5m is the second 5-minute slot, base+35m the eighth.
	if fine[1].Count != 1 || fine[7].Count != 1 {
		t.Fatalf("5m bucket counts: %+v", fine)
	}
}
