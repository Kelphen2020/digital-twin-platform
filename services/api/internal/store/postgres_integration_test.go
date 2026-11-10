package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

// TestPostgresContract exercises store paths that differ between the memory and
// PostgreSQL implementations. It is skipped unless PG_TEST_URL is set.
func TestPostgresContract(t *testing.T) {
	url := os.Getenv("PG_TEST_URL")
	if url == "" {
		t.Skip("PG_TEST_URL not set; skipping PostgreSQL integration test")
	}
	ctx := context.Background()
	pg, err := store.NewPostgres(ctx, url)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pg.Close()

	cameras, err := pg.ListCameras(ctx)
	if err != nil || len(cameras) == 0 {
		t.Fatalf("list cameras: %v (n=%d)", err, len(cameras))
	}
	camera := cameras[0]
	now := time.Now().UTC()
	suffix := now.Format("150405.000000")

	// 1) Escalation scan must round-trip every returned column.
	event := model.Event{
		ID: "evt_it_" + suffix, CameraID: camera.ID, CameraCode: camera.Code, CameraName: camera.Name,
		Type: "intrusion", Severity: "warn", Confidence: 0.9,
		OccurredAt: now.Add(-10 * time.Second), Status: "new",
	}
	if err := pg.CreateEvents(ctx, []model.Event{event}); err != nil {
		t.Fatalf("create event: %v", err)
	}
	escalated, err := pg.EscalateStale(ctx, now.Add(-5*time.Second), []string{"warn", "critical"})
	if err != nil {
		t.Fatalf("escalate stale: %v", err)
	}
	found := false
	for _, candidate := range escalated {
		if candidate.ID == event.ID && candidate.EscalatedAt != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("event %s was not returned with escalated_at", event.ID)
	}
	if _, err := pg.ReEscalateStale(ctx, now.Add(time.Minute), []string{"warn"}); err != nil {
		t.Fatalf("re-escalate stale: %v", err)
	}

	// 2) Orders may exist without an event (patrol-issued work orders).
	order := model.Order{
		ID: "ord_it_" + suffix, EventID: "", CameraID: camera.ID,
		Title: "巡检异常 · 集成测试", Status: "open", Priority: "normal",
		Assignee: "operator", Note: "integration", CreatedAt: now, UpdatedAt: now,
	}
	if err := pg.CreateOrder(ctx, order); err != nil {
		t.Fatalf("create patrol order without event: %v", err)
	}
	loaded, err := pg.GetOrder(ctx, order.ID)
	if err != nil || loaded.EventID != "" {
		t.Fatalf("load patrol order: %v event_id=%q", err, loaded.EventID)
	}

	// 3) User token_version must persist (session revocation).
	account := model.UserAccount{
		ID: "usr_it_" + suffix, Username: "it_" + suffix, DisplayName: "Integration",
		Role: "viewer", Areas: []string{}, Active: true, PasswordHash: "x",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := pg.CreateUserAccount(ctx, account); err != nil {
		t.Fatalf("create user: %v", err)
	}
	stored, ok, err := pg.GetUserAccountByID(ctx, account.ID)
	if err != nil || !ok {
		t.Fatalf("load user: %v ok=%v", err, ok)
	}
	stored.TokenVersion = 3
	if err := pg.UpdateUserAccount(ctx, stored); err != nil {
		t.Fatalf("update user: %v", err)
	}
	reread, ok, err := pg.GetUserAccountByID(ctx, account.ID)
	if err != nil || !ok || reread.TokenVersion != 3 {
		t.Fatalf("token_version not persisted: %+v err=%v", reread, err)
	}

	// 4) Notification retry attempts must persist.
	entry := model.NotificationLog{
		ID: "ntf_it_" + suffix, At: now, Kind: "escalation", EventID: event.ID,
		URL: "http://example.invalid/hook", Format: "generic", OK: true, StatusCode: 200, Attempts: 3,
	}
	if err := pg.CreateNotificationLog(ctx, entry); err != nil {
		t.Fatalf("create notification log: %v", err)
	}
	logs, err := pg.ListNotificationLog(ctx, 10)
	if err != nil {
		t.Fatalf("list notification log: %v", err)
	}
	attemptsFound := false
	for _, log := range logs {
		if log.ID == entry.ID && log.Attempts == 3 {
			attemptsFound = true
		}
	}
	if !attemptsFound {
		t.Fatalf("notification attempts not persisted: %+v", logs)
	}

	if err := pg.DeleteUserAccount(ctx, account.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
}
