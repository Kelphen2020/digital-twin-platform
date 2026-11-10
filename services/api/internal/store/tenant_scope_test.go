package store_test

import (
	"context"
	"testing"
	"time"

	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

// P28: duty groups and track points are tenant-scoped like the other entities.
func TestTenantScopedDutyGroupsAndTracks(t *testing.T) {
	memory := newMemory(t)
	ctxA := store.WithTenant(context.Background(), model.DefaultTenantID)
	ctxB := store.WithTenant(context.Background(), "tnt_test_b")

	// The default tenant is seeded by NewMemory; only tenant B needs creating.
	if err := memory.CreateTenant(context.Background(), model.Tenant{ID: "tnt_test_b", Name: "B", Active: true}); err != nil {
		t.Fatalf("create tenant B: %v", err)
	}

	dutyA := model.DutyGroup{ID: "duty_a", Name: "A 班组", Areas: []string{"area_gate"}, Active: true, CreatedAt: time.Now().UTC()}
	if err := memory.CreateDutyGroup(ctxA, dutyA); err != nil {
		t.Fatalf("create duty A: %v", err)
	}
	dutyB := model.DutyGroup{ID: "duty_b", Name: "B 班组", Active: true, CreatedAt: time.Now().UTC()}
	if err := memory.CreateDutyGroup(ctxB, dutyB); err != nil {
		t.Fatalf("create duty B: %v", err)
	}

	groupsA, err := memory.ListDutyGroups(ctxA)
	if err != nil || len(groupsA) != 1 || groupsA[0].ID != "duty_a" || groupsA[0].TenantID != model.DefaultTenantID {
		t.Fatalf("tenant A duty list: %v %+v", err, groupsA)
	}
	groupsB, err := memory.ListDutyGroups(ctxB)
	if err != nil || len(groupsB) != 1 || groupsB[0].ID != "duty_b" || groupsB[0].TenantID != "tnt_test_b" {
		t.Fatalf("tenant B duty list: %v %+v", err, groupsB)
	}
	if _, err := memory.GetDutyGroup(ctxB, "duty_a"); !store.IsNotFound(err) {
		t.Fatalf("tenant B must not read tenant A duty group: %v", err)
	}
	if err := memory.DeleteDutyGroup(ctxB, "duty_a"); !store.IsNotFound(err) {
		t.Fatalf("tenant B must not delete tenant A duty group: %v", err)
	}
	if _, err := memory.UpdateDutyGroup(ctxB, "duty_a", dutyA); !store.IsNotFound(err) {
		t.Fatalf("tenant B must not update tenant A duty group: %v", err)
	}

	now := time.Now().UTC()
	if err := memory.CreateTrackPoints(ctxA, []model.TrackPoint{
		{CameraID: "cam_001", TrackID: "trk-a", Class: "person", TS: now},
	}); err != nil {
		t.Fatalf("create tracks A: %v", err)
	}
	if err := memory.CreateTrackPoints(ctxB, []model.TrackPoint{
		{CameraID: "cam_001", TrackID: "trk-b", Class: "person", TS: now},
	}); err != nil {
		t.Fatalf("create tracks B: %v", err)
	}
	tracksA, err := memory.ListTrackPoints(ctxA, model.TrackFilter{})
	if err != nil || len(tracksA) != 1 || tracksA[0].TrackID != "trk-a" || tracksA[0].TenantID != model.DefaultTenantID {
		t.Fatalf("tenant A tracks: %v %+v", err, tracksA)
	}
	tracksB, err := memory.ListTrackPoints(ctxB, model.TrackFilter{})
	if err != nil || len(tracksB) != 1 || tracksB[0].TrackID != "trk-b" {
		t.Fatalf("tenant B tracks: %v %+v", err, tracksB)
	}
}
