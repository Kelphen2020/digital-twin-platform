package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

func newMemory(t *testing.T) *store.Memory {
	t.Helper()
	memory := store.NewMemory(100)
	memory.Seed(store.SeedCameras(), store.SeedAreas())
	return memory
}

func TestCameraAndAreaQueries(t *testing.T) {
	memory := newMemory(t)
	ctx := context.Background()

	areas, err := memory.ListAreas(ctx)
	if err != nil || len(areas) == 0 {
		t.Fatalf("areas: %v (%d)", err, len(areas))
	}
	cameras, err := memory.ListCameras(ctx)
	if err != nil || len(cameras) != 6 {
		t.Fatalf("cameras: %v (%d)", err, len(cameras))
	}
	camera, err := memory.GetCamera(ctx, cameras[0].ID)
	if err != nil || camera.Code != cameras[0].Code {
		t.Fatalf("get camera: %v", err)
	}
	byCode, err := memory.GetCameraByCode(ctx, cameras[1].Code)
	if err != nil || byCode.ID != cameras[1].ID {
		t.Fatalf("camera by code: %v", err)
	}
	if _, err := memory.GetCamera(ctx, "cam_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing camera: %v", err)
	}

	status := "offline"
	updated, err := memory.UpdateCamera(ctx, cameras[0].ID, model.CameraPatch{Status: &status})
	if err != nil || updated.Status != "offline" {
		t.Fatalf("update camera: %v %+v", err, updated)
	}
	updated, err = memory.UpdateCamera(ctx, cameras[0].ID, model.CameraPatch{Status: nil})
	if err != nil || updated.Status != "offline" {
		t.Fatalf("no-op patch should be 200: %v", err)
	}
	if _, err := memory.UpdateCamera(ctx, "cam_missing", model.CameraPatch{Status: &status}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("patch missing camera: %v", err)
	}

	counts, err := memory.Counts(ctx)
	if err != nil || counts.EventsTotal != 0 {
		t.Fatalf("counts: %v %+v", err, counts)
	}

	if _, err := memory.GetCameraRules(ctx, cameras[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("camera without rules should be not found: %v", err)
	}
	rules := model.DefaultCameraRules(cameras[0].ID)
	rules.ConfidenceThreshold = 0.9
	saved, err := memory.SetCameraRules(ctx, rules)
	if err != nil || saved.ConfidenceThreshold != 0.9 {
		t.Fatalf("set rules: %v %+v", err, saved)
	}
	if _, err := memory.SetCameraRules(ctx, model.CameraRules{CameraID: "cam_missing"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rules for missing camera: %v", err)
	}
}

func TestEventsOrdersTracksAudit(t *testing.T) {
	memory := newMemory(t)
	ctx := context.Background()
	now := time.Now().UTC()

	event := model.Event{
		ID: "evt_unit_1", CameraID: "cam_001", CameraCode: "CAM-001", CameraName: "东门入口",
		Type: "intrusion", Severity: "warn", Confidence: 0.9, OccurredAt: now, Status: "new",
	}
	if err := memory.CreateEvents(ctx, []model.Event{event}); err != nil {
		t.Fatalf("create events: %v", err)
	}
	events, err := memory.ListEvents(ctx, model.EventFilter{Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("list events: %v (%d)", err, len(events))
	}
	loaded, err := memory.GetEvent(ctx, event.ID)
	if err != nil || loaded.Severity != "warn" {
		t.Fatalf("get event: %v", err)
	}
	if _, err := memory.GetEvent(ctx, "evt_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing event: %v", err)
	}
	filtered, err := memory.ListEvents(ctx, model.EventFilter{Type: "vehicle", Limit: 10})
	if err != nil || len(filtered) != 0 {
		t.Fatalf("severity filter: %v (%d)", err, len(filtered))
	}

	if err := memory.CreateEventAction(ctx, model.EventAction{ID: "act_1", EventID: event.ID, Action: "ack", Actor: "operator", At: now}); err != nil {
		t.Fatalf("create action: %v", err)
	}
	if err := memory.CreateEventAction(ctx, model.EventAction{ID: "act_2", EventID: "evt_missing"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("action for missing event: %v", err)
	}
	actions, err := memory.ListEventActions(ctx, event.ID)
	if err != nil || len(actions) != 1 {
		t.Fatalf("list actions: %v (%d)", err, len(actions))
	}

	points := []model.TrackPoint{
		{CameraID: "cam_001", TrackID: "trk-1", Class: "person", X: 1, Y: 2, Z: 0, TS: now},
		{CameraID: "cam_001", TrackID: "trk-1", Class: "person", X: 2, Y: 3, Z: 0, TS: now.Add(time.Second)},
		{CameraID: "cam_002", TrackID: "trk-2", Class: "car", X: 5, Y: 6, Z: 0, TS: now},
	}
	if err := memory.CreateTrackPoints(ctx, points); err != nil {
		t.Fatalf("create tracks: %v", err)
	}
	tracks, err := memory.ListTrackPoints(ctx, model.TrackFilter{CameraID: "cam_001"})
	if err != nil || len(tracks) != 2 {
		t.Fatalf("tracks by camera: %v (%d)", err, len(tracks))
	}
	tracks, err = memory.ListTrackPoints(ctx, model.TrackFilter{CameraIDs: []string{"cam_001", "cam_002"}, Limit: 10})
	if err != nil || len(tracks) != 3 {
		t.Fatalf("tracks by ids: %v (%d)", err, len(tracks))
	}

	order := model.Order{
		ID: "ord_1", EventID: event.ID, CameraID: "cam_001", Title: "测试工单",
		Status: "open", Priority: "normal", Assignee: "operator", CreatedAt: now, UpdatedAt: now,
	}
	if err := memory.CreateOrder(ctx, order); err != nil {
		t.Fatalf("create order: %v", err)
	}
	openOrder, err := memory.GetOrder(ctx, order.ID)
	if err != nil || openOrder.Status != "open" {
		t.Fatalf("get order: %v", err)
	}
	done := "done"
	if _, err := memory.UpdateOrder(ctx, order.ID, model.OrderUpdate{Status: &done}); err != nil {
		t.Fatalf("update order: %v", err)
	}
	orders, err := memory.ListOrders(ctx, model.OrderFilter{Status: "done", Limit: 10})
	if err != nil || len(orders) != 1 {
		t.Fatalf("orders by status: %v (%d)", err, len(orders))
	}
	found, ok, err := memory.FindOpenOrderForEvent(ctx, event.ID)
	if err != nil || ok {
		t.Fatalf("no open order expected: %v ok=%v %+v", err, ok, found)
	}
	if _, err := memory.UpdateOrder(ctx, "ord_missing", model.OrderUpdate{Status: &done}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update missing order: %v", err)
	}

	_ = memory.AddAudit(ctx, model.AuditEntry{ID: "aud_1", At: now, Actor: "admin", Action: "test.action", Resource: "x"})
	_ = memory.AddAudit(ctx, model.AuditEntry{ID: "aud_2", At: now.Add(-time.Hour), Actor: "operator", Action: "other.action"})
	audits, err := memory.ListAudit(ctx, model.AuditFilter{Actor: "admin", Limit: 10})
	if err != nil || len(audits) != 1 {
		t.Fatalf("audit filter: %v (%d)", err, len(audits))
	}
	from := now.Add(-time.Minute)
	audits, err = memory.ListAudit(ctx, model.AuditFilter{From: &from, Limit: 10})
	if err != nil || len(audits) != 1 {
		t.Fatalf("audit range: %v (%d)", err, len(audits))
	}

	summary, err := memory.Stats(ctx, now.Add(-time.Hour), now.Add(time.Hour), nil)
	if err != nil || summary.Totals.Events != 1 {
		t.Fatalf("stats: %v %+v", err, summary)
	}
}

func TestSettingsDutyAndUsers(t *testing.T) {
	memory := newMemory(t)
	ctx := context.Background()

	if _, ok, err := memory.GetSetting(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing setting: %v ok=%v", err, ok)
	}
	if err := memory.SetSetting(ctx, "escalation", []byte(`{"enabled":true}`)); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	value, ok, err := memory.GetSetting(ctx, "escalation")
	if err != nil || !ok || len(value) == 0 {
		t.Fatalf("get setting: %v ok=%v", err, ok)
	}

	now := time.Now().UTC()
	group := model.DutyGroup{ID: "duty_1", Name: "测试班组", Areas: []string{"area_gate"}, Operators: []string{"operator"}, Active: true, CreatedAt: now}
	if err := memory.CreateDutyGroup(ctx, group); err != nil {
		t.Fatalf("create duty: %v", err)
	}
	if _, err := memory.GetDutyGroup(ctx, "duty_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing duty group: %v", err)
	}
	groups, err := memory.ListDutyGroups(ctx)
	if err != nil || len(groups) == 0 {
		t.Fatalf("list duty: %v", err)
	}
	updated, err := memory.UpdateDutyGroup(ctx, "duty_1", model.DutyGroup{Name: "改", Areas: group.Areas, Active: true})
	if err != nil || updated.Name != "改" {
		t.Fatalf("update duty: %v %+v", err, updated)
	}
	if err := memory.DeleteDutyGroup(ctx, "duty_1"); err != nil {
		t.Fatalf("delete duty: %v", err)
	}
	if err := memory.DeleteDutyGroup(ctx, "duty_1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}

	account := model.UserAccount{ID: "usr_1", Username: "unit_user", Role: "viewer", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := memory.CreateUserAccount(ctx, account); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := memory.CreateUserAccount(ctx, account); err != model.ErrConflict {
		t.Fatalf("duplicate user: %v", err)
	}
	if _, ok, err := memory.GetUserAccountByUsername(ctx, "unit_user"); err != nil || !ok {
		t.Fatalf("user by name: %v ok=%v", err, ok)
	}
	account.Role = "operator"
	if err := memory.UpdateUserAccount(ctx, account); err != nil {
		t.Fatalf("update user: %v", err)
	}
	stored, ok, err := memory.GetUserAccountByID(ctx, "usr_1")
	if err != nil || !ok || stored.Role != "operator" {
		t.Fatalf("user by id: %v %+v", err, stored)
	}
	if err := memory.DeleteUserAccount(ctx, "usr_1"); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if err := memory.DeleteUserAccount(ctx, "usr_1"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("delete user twice: %v", err)
	}

	// Notification log trimming and refresh-token lifecycle.
	for index := 0; index < 3; index++ {
		_ = memory.CreateNotificationLog(ctx, model.NotificationLog{ID: "ntf_" + string(rune('a'+index)), At: now.Add(time.Duration(index) * time.Second)})
	}
	logs, err := memory.ListNotificationLog(ctx, 2)
	if err != nil || len(logs) != 2 {
		t.Fatalf("notification log: %v (%d)", err, len(logs))
	}
	token := model.RefreshToken{ID: "rft_1", UserID: "usr_x", TokenHash: "hash", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := memory.CreateRefreshToken(ctx, token); err != nil {
		t.Fatalf("create refresh: %v", err)
	}
	if _, ok, err := memory.GetRefreshTokenByHash(ctx, "hash"); err != nil || !ok {
		t.Fatalf("get refresh: %v ok=%v", err, ok)
	}
	if err := memory.RevokeRefreshToken(ctx, "rft_missing"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("revoke missing: %v", err)
	}
	if err := memory.RevokeUserRefreshTokens(ctx, "usr_x"); err != nil {
		t.Fatalf("revoke user refresh: %v", err)
	}
}
