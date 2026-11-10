package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

// tenantCtx scopes a background context to one tenant.
func tenantCtx(id string) context.Context {
	return store.WithTenant(context.Background(), id)
}

func seedTenant(t *testing.T, memory *store.Memory, id string) {
	t.Helper()
	if err := memory.CreateTenant(context.Background(), model.Tenant{
		ID: id, Name: "租户 " + id, Active: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create tenant %s: %v", id, err)
	}
}

func TestTenantCRUDAndDefaultSeed(t *testing.T) {
	memory := store.NewMemory(100)
	ctx := context.Background()

	tenants, err := memory.ListTenants(ctx)
	if err != nil || len(tenants) != 1 || tenants[0].ID != model.DefaultTenantID || !tenants[0].Active {
		t.Fatalf("default tenant seed: %v %+v", err, tenants)
	}
	tenant, ok, err := memory.GetTenant(ctx, model.DefaultTenantID)
	if err != nil || !ok || tenant.Name == "" {
		t.Fatalf("get default tenant: %v %v %+v", err, ok, tenant)
	}

	seedTenant(t, memory, "tenant_b")
	tenantB, ok, err := memory.GetTenant(ctx, "tenant_b")
	if err != nil || !ok || tenantB.Active != true {
		t.Fatalf("get tenant_b: %v %v %+v", err, ok, tenantB)
	}
	duplicate := model.Tenant{ID: "tenant_b", Name: "dup", Active: true}
	if err := memory.CreateTenant(ctx, duplicate); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate tenant should conflict, got %v", err)
	}

	tenantB.Active = false
	tenantB.Name = "停用租户"
	if err := memory.UpdateTenant(ctx, tenantB); err != nil {
		t.Fatalf("update tenant: %v", err)
	}
	updated, _, _ := memory.GetTenant(ctx, "tenant_b")
	if updated.Active || updated.Name != "停用租户" {
		t.Fatalf("tenant update not persisted: %+v", updated)
	}
	if _, ok, _ := memory.GetTenant(ctx, "tenant_missing"); ok {
		t.Fatal("missing tenant should not be found")
	}
}

func TestTenantIsolationEntities(t *testing.T) {
	memory := newMemory(t)
	seedTenant(t, memory, "tenant_b")
	ctxA := tenantCtx(model.DefaultTenantID)
	ctxB := tenantCtx("tenant_b")

	// cameras/areas seeded only for the default tenant.
	camerasA, err := memory.ListCameras(ctxA)
	if err != nil || len(camerasA) != 6 {
		t.Fatalf("default cameras: %v (%d)", err, len(camerasA))
	}
	camerasB, err := memory.ListCameras(ctxB)
	if err != nil || len(camerasB) != 0 {
		t.Fatalf("tenant_b cameras should be empty: %v (%d)", err, len(camerasB))
	}
	if _, err := memory.GetCamera(ctxB, camerasA[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant camera should be not found, got %v", err)
	}
	if _, err := memory.GetCameraByCode(ctxB, camerasA[0].Code); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant camera by code should be not found, got %v", err)
	}

	// users: same username is allowed per tenant, invisible across tenants.
	userA := model.UserAccount{ID: "usr_a", Username: "same", DisplayName: "A", Role: "viewer", Active: true}
	userB := model.UserAccount{ID: "usr_b", Username: "same", DisplayName: "B", Role: "viewer", Active: true}
	if err := memory.CreateUserAccount(ctxA, userA); err != nil {
		t.Fatalf("create user A: %v", err)
	}
	if err := memory.CreateUserAccount(ctxB, userB); err != nil {
		t.Fatalf("create same username in tenant_b: %v", err)
	}
	if err := memory.CreateUserAccount(ctxB, userB); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate username within tenant should conflict, got %v", err)
	}
	if got, ok, _ := memory.GetUserAccountByUsername(ctxA, "same"); !ok || got.ID != "usr_a" {
		t.Fatalf("tenant A user lookup: %v %+v", ok, got)
	}
	if got, ok, _ := memory.GetUserAccountByUsername(ctxB, "same"); !ok || got.ID != "usr_b" {
		t.Fatalf("tenant B user lookup: %v %+v", ok, got)
	}
	if _, ok, _ := memory.GetUserAccountByID(ctxB, "usr_a"); ok {
		t.Fatal("tenant B must not resolve an id from tenant A")
	}
	accountsA, _ := memory.ListUserAccounts(ctxA)
	if len(accountsA) != 1 || accountsA[0].ID != "usr_a" {
		t.Fatalf("tenant A users: %+v", accountsA)
	}

	// settings are keyed per tenant.
	if err := memory.SetSetting(ctxA, "escalation", []byte(`{"tenant":"a"}`)); err != nil {
		t.Fatalf("set setting A: %v", err)
	}
	if err := memory.SetSetting(ctxB, "escalation", []byte(`{"tenant":"b"}`)); err != nil {
		t.Fatalf("set setting B: %v", err)
	}
	valueA, ok, _ := memory.GetSetting(ctxA, "escalation")
	if !ok || string(valueA) != `{"tenant":"a"}` {
		t.Fatalf("tenant A setting: %v %s", ok, valueA)
	}
	valueB, ok, _ := memory.GetSetting(ctxB, "escalation")
	if !ok || string(valueB) != `{"tenant":"b"}` {
		t.Fatalf("tenant B setting: %v %s", ok, valueB)
	}

	// events/orders are invisible across tenants.
	now := time.Now().UTC()
	eventA := model.Event{ID: "evt_a", CameraID: "cam_001", Type: "intrusion", Severity: "warn", OccurredAt: now, Status: "new"}
	eventB := model.Event{ID: "evt_b", CameraID: "cam_001", Type: "intrusion", Severity: "warn", OccurredAt: now, Status: "new"}
	if err := memory.CreateEvents(ctxA, []model.Event{eventA}); err != nil {
		t.Fatalf("create event A: %v", err)
	}
	if err := memory.CreateEvents(ctxB, []model.Event{eventB}); err != nil {
		t.Fatalf("create event B: %v", err)
	}
	eventsA, _ := memory.ListEvents(ctxA, model.EventFilter{})
	if len(eventsA) != 1 || eventsA[0].ID != "evt_a" {
		t.Fatalf("tenant A events: %+v", eventsA)
	}
	if _, err := memory.GetEvent(ctxB, "evt_a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("tenant B must not read tenant A event: %v", err)
	}
	orderB := model.Order{ID: "ord_b", EventID: "evt_b", CameraID: "cam_001", Title: "b", Status: "open", CreatedAt: now, UpdatedAt: now}
	if err := memory.CreateOrder(ctxB, orderB); err != nil {
		t.Fatalf("create order B: %v", err)
	}
	if _, err := memory.GetOrder(ctxA, "ord_b"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("tenant A must not read tenant B order: %v", err)
	}
	ordersA, _ := memory.ListOrders(ctxA, model.OrderFilter{})
	if len(ordersA) != 0 {
		t.Fatalf("tenant A orders should be empty: %+v", ordersA)
	}

	// audit log is tenant scoped.
	_ = memory.AddAudit(ctxA, model.AuditEntry{ID: "aud_a", At: now, Actor: "admin", Action: "tenant.create", Resource: "a"})
	_ = memory.AddAudit(ctxB, model.AuditEntry{ID: "aud_b", At: now, Actor: "admin", Action: "tenant.create", Resource: "b"})
	auditA, _ := memory.ListAudit(ctxA, model.AuditFilter{})
	if len(auditA) != 1 || auditA[0].ID != "aud_a" {
		t.Fatalf("tenant A audit: %+v", auditA)
	}
	auditB, _ := memory.ListAudit(ctxB, model.AuditFilter{})
	if len(auditB) != 1 || auditB[0].ID != "aud_b" {
		t.Fatalf("tenant B audit: %+v", auditB)
	}

	// patrol routes/schedules/executions are tenant scoped.
	routeB := model.PatrolRoute{ID: "patrol_b", Name: "b", CreatedAt: now, UpdatedAt: now}
	scheduleB := model.PatrolSchedule{ID: "psc_b", Name: "b", RouteID: "patrol_b", CreatedAt: now, UpdatedAt: now, NextRunAt: now}
	executionB := model.PatrolExecution{ID: "pex_b", RouteID: "patrol_b", Status: "pending", CreatedAt: now, ScheduledAt: now}
	if err := memory.CreatePatrolRoute(ctxB, routeB); err != nil {
		t.Fatalf("create patrol route B: %v", err)
	}
	if err := memory.CreatePatrolSchedule(ctxB, scheduleB); err != nil {
		t.Fatalf("create patrol schedule B: %v", err)
	}
	if err := memory.CreatePatrolExecution(ctxB, executionB); err != nil {
		t.Fatalf("create patrol execution B: %v", err)
	}
	if routes, _ := memory.ListPatrolRoutes(ctxA); len(routes) != 0 {
		t.Fatalf("tenant A must not see tenant B routes: %+v", routes)
	}
	if _, ok, _ := memory.GetPatrolRoute(ctxA, "patrol_b"); ok {
		t.Fatal("tenant A must not get tenant B route")
	}
	if schedules, _ := memory.ListPatrolSchedules(ctxA); len(schedules) != 0 {
		t.Fatalf("tenant A must not see tenant B schedules: %+v", schedules)
	}
	if executions, _ := memory.ListPatrolExecutions(ctxA, model.PatrolExecutionFilter{}); len(executions) != 0 {
		t.Fatalf("tenant A must not see tenant B executions: %+v", executions)
	}
	if _, ok, _ := memory.GetPatrolExecution(ctxB, "pex_b"); !ok {
		t.Fatal("tenant B should see its own execution")
	}
}

func TestTenantSharedMode(t *testing.T) {
	t.Setenv("TENANT_SHARED", "1")
	memory := newMemory(t)
	seedTenant(t, memory, "tenant_b")
	ctxB := tenantCtx("tenant_b")

	// All tenants read the default tenant's data view.
	cameras, err := memory.ListCameras(ctxB)
	if err != nil || len(cameras) != 6 {
		t.Fatalf("shared mode cameras via tenant_b: %v (%d)", err, len(cameras))
	}
	now := time.Now().UTC()
	order := model.Order{ID: "ord_shared", CameraID: "cam_001", Title: "shared", Status: "open", CreatedAt: now, UpdatedAt: now}
	if err := memory.CreateOrder(ctxB, order); err != nil {
		t.Fatalf("shared create order: %v", err)
	}
	orders, err := memory.ListOrders(context.Background(), model.OrderFilter{})
	if err != nil || len(orders) != 1 || orders[0].ID != "ord_shared" {
		t.Fatalf("shared order visibility: %v %+v", err, orders)
	}
	if err := memory.SetSetting(ctxB, "escalation", []byte(`{"shared":true}`)); err != nil {
		t.Fatalf("shared set setting: %v", err)
	}
	value, ok, err := memory.GetSetting(context.Background(), "escalation")
	if err != nil || !ok || string(value) != `{"shared":true}` {
		t.Fatalf("shared setting visibility: %v %v %s", err, ok, value)
	}
}
