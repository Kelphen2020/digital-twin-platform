package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

// TestPostgresStoreContract exercises the PostgreSQL store across tenants so the
// SQL paths (not just the in-memory ones) stay covered. Skipped without
// PG_TEST_URL.
func TestPostgresStoreContract(t *testing.T) {
	url := os.Getenv("PG_TEST_URL")
	if url == "" {
		t.Skip("PG_TEST_URL not set; skipping PostgreSQL contract test")
	}
	ctx0 := context.Background()
	pg, err := store.NewPostgres(ctx0, url)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pg.Close()

	now := time.Now().UTC()
	suffix := now.Format("150405.000000000")
	tenantID := "tnt_it_" + suffix
	if err := pg.CreateTenant(ctx0, model.Tenant{ID: tenantID, Name: "集成测试租户", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pg.DeleteTenant(ctx0, tenantID) })
	ctx := store.WithTenant(ctx0, tenantID)

	// --- areas + cameras ---------------------------------------------------
	area := model.Area{ID: "area_it_" + suffix, Name: "测试区域", Type: "zone",
		Polygon: [][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}}
	if err := pg.CreateArea(ctx, area); err != nil {
		t.Fatalf("create area: %v", err)
	}
	areas, err := pg.ListAreas(ctx)
	if err != nil || len(areas) != 1 || areas[0].ID != area.ID {
		t.Fatalf("list areas: %v %+v", err, areas)
	}

	stream := "/hls/test.m3u8"
	camera := model.Camera{
		ID: "cam_it_" + suffix, Code: "IT-001", Name: "集成相机", AreaID: area.ID,
		Status: "online", StreamURL: &stream, UpdatedAt: now,
	}
	if err := pg.CreateCamera(ctx, camera); err != nil {
		t.Fatalf("create camera: %v", err)
	}
	if err := pg.CreateCamera(ctx, camera); !store.IsConflict(err) {
		t.Fatalf("duplicate camera code must conflict, got %v", err)
	}
	cameras, err := pg.ListCameras(ctx)
	if err != nil || len(cameras) != 1 {
		t.Fatalf("list cameras: %v n=%d", err, len(cameras))
	}
	byCode, err := pg.GetCameraByCode(ctx, camera.Code)
	if err != nil || byCode.ID != camera.ID {
		t.Fatalf("get camera by code: %v %+v", err, byCode)
	}
	offline := "offline"
	patched, err := pg.UpdateCamera(ctx, camera.ID, model.CameraPatch{Status: &offline})
	if err != nil || patched.Status != "offline" {
		t.Fatalf("update camera: %v %+v", err, patched)
	}
	calibration := &model.Calibration{Matrix: [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}}
	if _, err := pg.SetCameraCalibration(ctx, camera.ID, calibration); err != nil {
		t.Fatalf("set calibration: %v", err)
	}

	// --- events, actions, tracks ------------------------------------------
	event := model.Event{
		ID: "evt_it_" + suffix, CameraID: camera.ID, CameraCode: camera.Code, CameraName: camera.Name,
		Type: "intrusion", Severity: "warn", Confidence: 0.91,
		OccurredAt: now.Add(-time.Minute), Status: "new",
	}
	if err := pg.CreateEvents(ctx, []model.Event{event}); err != nil {
		t.Fatalf("create event: %v", err)
	}
	events, err := pg.ListEvents(ctx, model.EventFilter{Limit: 10})
	if err != nil || len(events) != 1 || events[0].ID != event.ID {
		t.Fatalf("list events: %v %+v", err, events)
	}
	acked, err := pg.AckEvent(ctx, event.ID)
	if err != nil || acked.Status != "acked" {
		t.Fatalf("ack event: %v %+v", err, acked)
	}
	if _, err := pg.CloseEvent(ctx, event.ID); err != nil {
		t.Fatalf("close event: %v", err)
	}
	action := model.EventAction{ID: "act_it_" + suffix, EventID: event.ID, Action: "dispatch",
		Actor: "operator", Target: "运营", Note: "集成", At: now}
	if err := pg.CreateEventAction(ctx, action); err != nil {
		t.Fatalf("create action: %v", err)
	}
	actions, err := pg.ListEventActions(ctx, event.ID)
	if err != nil || len(actions) != 1 {
		t.Fatalf("list actions: %v n=%d", err, len(actions))
	}
	if err := pg.CreateTrackPoints(ctx, []model.TrackPoint{
		{CameraID: camera.ID, TrackID: "trk_it", Class: "person", X: 1, Y: 2, TS: now},
	}); err != nil {
		t.Fatalf("create track: %v", err)
	}
	tracks, err := pg.ListTrackPoints(ctx, model.TrackFilter{Limit: 10})
	if err != nil || len(tracks) != 1 || tracks[0].TenantID != tenantID {
		t.Fatalf("list tracks: %v %+v", err, tracks)
	}

	// --- rules, orders, settings, audit, notifications --------------------
	if _, err := pg.SetCameraRules(ctx, model.DefaultCameraRules(camera.ID)); err != nil {
		t.Fatalf("set rules: %v", err)
	}
	if _, err := pg.GetCameraRules(ctx, camera.ID); err != nil {
		t.Fatalf("get rules: %v", err)
	}
	order := model.Order{ID: "ord_it_" + suffix, EventID: event.ID, CameraID: camera.ID,
		Title: "集成工单", Status: "open", Priority: "high", CreatedAt: now, UpdatedAt: now}
	if err := pg.CreateOrder(ctx, order); err != nil {
		t.Fatalf("create order: %v", err)
	}
	open, found, err := pg.FindOpenOrderForEvent(ctx, event.ID)
	if err != nil || !found || open.ID != order.ID {
		t.Fatalf("find open order: %v found=%v", err, found)
	}
	ackStatus := "acknowledged"
	if _, err := pg.UpdateOrder(ctx, order.ID, model.OrderUpdate{Status: &ackStatus}); err != nil {
		t.Fatalf("update order: %v", err)
	}
	if orders, err := pg.ListOrders(ctx, model.OrderFilter{Limit: 10}); err != nil || len(orders) != 1 {
		t.Fatalf("list orders: %v n=%d", err, len(orders))
	}
	if err := pg.SetSetting(ctx, "notifications", []byte(`{"webhook_url":"http://x"}`)); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	if raw, ok, err := pg.GetSetting(ctx, "notifications"); err != nil || !ok || len(raw) == 0 {
		t.Fatalf("get setting: %v ok=%v", err, ok)
	}
	if err := pg.AddAudit(ctx, model.AuditEntry{ID: "aud_it_" + suffix, At: now, Actor: "admin",
		Action: "it.test", Resource: "test", Detail: map[string]any{"n": 1}}); err != nil {
		t.Fatalf("add audit: %v", err)
	}
	if entries, err := pg.ListAudit(ctx, model.AuditFilter{Limit: 10}); err != nil || len(entries) != 1 {
		t.Fatalf("list audit: %v n=%d", err, len(entries))
	}
	if err := pg.CreateNotificationLog(ctx, model.NotificationLog{ID: "ntf_it_" + suffix, At: now,
		Kind: "escalation", EventID: event.ID, URL: "http://example.invalid", Format: "generic", OK: true}); err != nil {
		t.Fatalf("create notification: %v", err)
	}

	// --- duty groups -------------------------------------------------------
	group := model.DutyGroup{ID: "duty_it_" + suffix, Name: "集成班组", Active: true,
		Areas: []string{area.ID}, CreatedAt: now, UpdatedAt: now}
	if err := pg.CreateDutyGroup(ctx, group); err != nil {
		t.Fatalf("create duty group: %v", err)
	}
	if loaded, err := pg.GetDutyGroup(ctx, group.ID); err != nil || loaded.TenantID != tenantID {
		t.Fatalf("get duty group: %v %+v", err, loaded)
	}
	if groups, err := pg.ListDutyGroups(ctx); err != nil || len(groups) != 1 {
		t.Fatalf("list duty groups: %v n=%d", err, len(groups))
	}
	if _, err := pg.UpdateDutyGroup(ctx, group.ID, group); err != nil {
		t.Fatalf("update duty group: %v", err)
	}

	// --- users + refresh tokens -------------------------------------------
	account := model.UserAccount{ID: "usr_it2_" + suffix, Username: "it2_" + suffix,
		DisplayName: "集成用户", Role: "viewer", Active: true, PasswordHash: "x",
		CreatedAt: now, UpdatedAt: now}
	if err := pg.CreateUserAccount(ctx, account); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if accounts, err := pg.ListUserAccounts(ctx); err != nil || len(accounts) != 1 {
		t.Fatalf("list users: %v n=%d", err, len(accounts))
	}
	if got, ok, err := pg.GetUserAccountByUsername(ctx, account.Username); err != nil || !ok || got.ID != account.ID {
		t.Fatalf("get user by username: %v ok=%v", err, ok)
	}
	token := model.RefreshToken{ID: "rt_it_" + suffix, UserID: account.ID, TokenHash: "hash_" + suffix,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := pg.CreateRefreshToken(ctx, token); err != nil {
		t.Fatalf("create refresh token: %v", err)
	}
	if _, ok, err := pg.GetRefreshTokenByHash(ctx, token.TokenHash); err != nil || !ok {
		t.Fatalf("get refresh token: %v ok=%v", err, ok)
	}

	// --- patrol ------------------------------------------------------------
	route := model.PatrolRoute{ID: "pr_it_" + suffix, Name: "集成路线", CameraIDs: []string{camera.ID},
		Waypoints: []model.Waypoint{{X: 1, Y: 2, Heading: 0}}, IntervalSeconds: 60,
		CreatedBy: "admin", CreatedAt: now, UpdatedAt: now}
	if err := pg.CreatePatrolRoute(ctx, route); err != nil {
		t.Fatalf("create patrol route: %v", err)
	}
	if routes, err := pg.ListPatrolRoutes(ctx); err != nil || len(routes) != 1 {
		t.Fatalf("list patrol routes: %v n=%d", err, len(routes))
	}

	// --- counts ------------------------------------------------------------
	counts, err := pg.Counts(ctx)
	if err != nil || counts.EventsTotal != 1 {
		t.Fatalf("counts: %v %+v", err, counts)
	}

	// --- tenant delete cleans everything ----------------------------------
	deleted, err := pg.DeleteTenant(ctx0, tenantID)
	if err != nil {
		t.Fatalf("delete tenant: %v", err)
	}
	if deleted["cameras"] != 1 || deleted["events"] != 1 {
		t.Fatalf("unexpected delete counts: %+v", deleted)
	}
	if _, ok, _ := pg.GetTenant(ctx0, tenantID); ok {
		t.Fatal("tenant still present after delete")
	}
}

// TestPostgresExportJobsContract covers the export job table.
func TestPostgresExportJobsContract(t *testing.T) {
	url := os.Getenv("PG_TEST_URL")
	if url == "" {
		t.Skip("PG_TEST_URL not set; skipping PostgreSQL export job test")
	}
	ctx := context.Background()
	pg, err := store.NewPostgres(ctx, url)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pg.Close()

	now := time.Now().UTC()
	id := "exp_it_" + now.Format("150405.000000000")
	job := model.ExportJob{ID: id, TenantID: "default", Status: model.ExportPending,
		IncludeAudit: true, CreatedBy: "admin", CreatedAt: now, Counts: map[string]int{}}
	if err := pg.CreateExportJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	t.Cleanup(func() { _ = pg.DeleteExportJob(ctx, id) })

	if err := pg.CreateExportJob(ctx, job); !store.IsConflict(err) {
		t.Fatalf("duplicate job must conflict, got %v", err)
	}
	job.Status = model.ExportDone
	job.SizeBytes = 42
	job.Counts = map[string]int{"cameras": 1}
	if err := pg.UpdateExportJob(ctx, job); err != nil {
		t.Fatalf("update job: %v", err)
	}
	loaded, ok, err := pg.GetExportJob(ctx, id)
	if err != nil || !ok || loaded.SizeBytes != 42 || loaded.Counts["cameras"] != 1 {
		t.Fatalf("get job: %v ok=%v %+v", err, ok, loaded)
	}
	jobs, err := pg.ListExportJobs(ctx, "default", 10)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("list jobs: %v n=%d", err, len(jobs))
	}
	removed, err := pg.PurgeExportJobs(ctx, "default", now.Add(time.Hour), 0)
	if err != nil || len(removed) == 0 {
		t.Fatalf("purge jobs: %v %v", err, removed)
	}
	if _, ok, _ := pg.GetExportJob(ctx, id); ok {
		t.Fatal("purged job still present")
	}
	if _, err := pg.FailPendingExportJobs(ctx, "test"); err != nil {
		t.Fatalf("fail pending jobs: %v", err)
	}
}

// TestPostgresTenantAndPatrolLifecycle covers tenant listing/updating plus the
// patrol and refresh-token CRUD paths on PostgreSQL.
func TestPostgresTenantAndPatrolLifecycle(t *testing.T) {
	url := os.Getenv("PG_TEST_URL")
	if url == "" {
		t.Skip("PG_TEST_URL not set; skipping PostgreSQL lifecycle test")
	}
	ctx0 := context.Background()
	pg, err := store.NewPostgres(ctx0, url)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer pg.Close()

	now := time.Now().UTC()
	suffix := now.Format("150405.000000000")
	tenantID := "tnt_lc_" + suffix
	if err := pg.CreateTenant(ctx0, model.Tenant{ID: tenantID, Name: "生命周期", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pg.DeleteTenant(ctx0, tenantID) })

	tenants, err := pg.ListTenants(ctx0)
	if err != nil || len(tenants) == 0 {
		t.Fatalf("list tenants: %v n=%d", err, len(tenants))
	}
	tenant, ok, err := pg.GetTenant(ctx0, tenantID)
	if err != nil || !ok {
		t.Fatalf("get tenant: %v ok=%v", err, ok)
	}
	tenant.Name = "生命周期改名"
	tenant.Archived = true
	tenant.Limits = model.TenantLimits{MaxUsers: 5}
	tenant.UpdatedAt = time.Now().UTC()
	if err := pg.UpdateTenant(ctx0, tenant); err != nil {
		t.Fatalf("update tenant: %v", err)
	}
	reread, _, _ := pg.GetTenant(ctx0, tenantID)
	if reread.Name != "生命周期改名" || !reread.Archived || reread.Limits.MaxUsers != 5 {
		t.Fatalf("tenant update not persisted: %+v", reread)
	}

	ctx := store.WithTenant(ctx0, tenantID)
	stream := "/hls/test.m3u8"
	camera := model.Camera{ID: "cam_lc_" + suffix, Code: "LC-001", Name: "生命周期相机",
		Status: "online", StreamURL: &stream, UpdatedAt: now}
	if err := pg.CreateCamera(ctx, camera); err != nil {
		t.Fatalf("create camera: %v", err)
	}
	if counts, err := pg.Counts(ctx); err != nil || counts.OnlineCameras != 1 {
		t.Fatalf("counts: %v %+v", err, counts)
	}
	if stats, err := pg.Stats(ctx, now.Add(-time.Hour), now.Add(time.Hour), nil); err != nil {
		t.Fatalf("stats: %v %+v", err, stats)
	}

	// Duty group lifecycle including delete.
	group := model.DutyGroup{ID: "duty_lc_" + suffix, Name: "生命周期班组", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := pg.CreateDutyGroup(ctx, group); err != nil {
		t.Fatalf("create duty: %v", err)
	}
	if _, err := pg.UpdateDutyGroup(ctx, group.ID, group); err != nil {
		t.Fatalf("update duty: %v", err)
	}
	if err := pg.DeleteDutyGroup(ctx, group.ID); err != nil {
		t.Fatalf("delete duty: %v", err)
	}
	if _, err := pg.GetDutyGroup(ctx, group.ID); !store.IsNotFound(err) {
		t.Fatalf("duty group should be gone, got %v", err)
	}

	// Patrol route CRUD.
	route := model.PatrolRoute{ID: "pr_lc_" + suffix, Name: "生命周期路线", CameraIDs: []string{camera.ID},
		Waypoints: []model.Waypoint{{X: 1, Y: 2}}, IntervalSeconds: 60, CreatedBy: "admin", CreatedAt: now, UpdatedAt: now}
	if err := pg.CreatePatrolRoute(ctx, route); err != nil {
		t.Fatalf("create route: %v", err)
	}
	if loaded, ok, err := pg.GetPatrolRoute(ctx, route.ID); err != nil || !ok || loaded.Name != route.Name {
		t.Fatalf("get route: %v ok=%v", err, ok)
	}
	route.Name = "生命周期路线2"
	if err := pg.UpdatePatrolRoute(ctx, route); err != nil {
		t.Fatalf("update route: %v", err)
	}
	if err := pg.DeletePatrolRoute(ctx, route.ID); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	if _, ok, _ := pg.GetPatrolRoute(ctx, route.ID); ok {
		t.Fatal("route should be deleted")
	}

	// Refresh token revocation.
	account := model.UserAccount{ID: "usr_lc_" + suffix, Username: "lc_" + suffix, DisplayName: "生命周期用户",
		Role: "viewer", Active: true, PasswordHash: "x", CreatedAt: now, UpdatedAt: now}
	if err := pg.CreateUserAccount(ctx, account); err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := model.RefreshToken{ID: "rt_lc_" + suffix, UserID: account.ID, TokenHash: "hash_lc_" + suffix,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := pg.CreateRefreshToken(ctx, token); err != nil {
		t.Fatalf("create refresh token: %v", err)
	}
	if tokens, err := pg.ListUserRefreshTokens(ctx, account.ID); err != nil || len(tokens) != 1 {
		t.Fatalf("list refresh tokens: %v n=%d", err, len(tokens))
	}
	if err := pg.RevokeRefreshToken(ctx, token.ID); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	if revoked, ok, _ := pg.GetRefreshTokenByHash(ctx, token.TokenHash); ok && !revoked.Revoked {
		t.Fatal("token should be revoked")
	}
	if err := pg.RevokeUserRefreshTokens(ctx, account.ID); err != nil {
		t.Fatalf("revoke user tokens: %v", err)
	}
}
