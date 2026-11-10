package store_test

import (
	"context"
	"testing"
	"time"

	"digital-twin-platform/api/internal/model"
	"digital-twin-platform/api/internal/store"
)

func TestPatrolRoutesAndSchedules(t *testing.T) {
	memory := newMemory(t)
	ctx := context.Background()
	now := time.Now().UTC()

	route := model.PatrolRoute{
		ID: "patrol_1", Name: "测试路线", Waypoints: []model.Waypoint{{X: 1, Y: 2, Z: 3}},
		CameraIDs: []string{"cam_001"}, IntervalSeconds: 3, CreatedBy: "operator", CreatedAt: now, UpdatedAt: now,
	}
	if err := memory.CreatePatrolRoute(ctx, route); err != nil {
		t.Fatalf("create route: %v", err)
	}
	loaded, ok, err := memory.GetPatrolRoute(ctx, "patrol_1")
	if err != nil || !ok || loaded.Name != "测试路线" {
		t.Fatalf("get route: %v ok=%v %+v", err, ok, loaded)
	}
	if _, ok, _ := memory.GetPatrolRoute(ctx, "patrol_missing"); ok {
		t.Fatal("missing route should report not found")
	}
	routes, err := memory.ListPatrolRoutes(ctx)
	if err != nil || len(routes) != 1 {
		t.Fatalf("list routes: %v (%d)", err, len(routes))
	}
	loaded.Name = "改后"
	if err := memory.UpdatePatrolRoute(ctx, loaded); err != nil {
		t.Fatalf("update route: %v", err)
	}
	if err := memory.UpdatePatrolRoute(ctx, model.PatrolRoute{ID: "patrol_missing"}); !store.IsNotFound(err) {
		t.Fatalf("update missing route: %v", err)
	}
	if err := memory.DeletePatrolRoute(ctx, "patrol_missing"); !store.IsNotFound(err) {
		t.Fatalf("delete missing route: %v", err)
	}

	schedule := model.PatrolSchedule{
		ID: "psc_1", Name: "测试计划", RouteID: "patrol_1", IntervalMinutes: 60, Enabled: true,
		CreatedBy: "operator", CreatedAt: now, UpdatedAt: now, NextRunAt: now.Add(time.Hour),
	}
	if err := memory.CreatePatrolSchedule(ctx, schedule); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if _, ok, err := memory.GetPatrolSchedule(ctx, "psc_1"); err != nil || !ok {
		t.Fatalf("get schedule: %v ok=%v", err, ok)
	}
	if _, ok, _ := memory.GetPatrolSchedule(ctx, "psc_missing"); ok {
		t.Fatal("missing schedule should report not found")
	}
	schedules, err := memory.ListPatrolSchedules(ctx)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("list schedules: %v (%d)", err, len(schedules))
	}
	schedule.IntervalMinutes = 120
	if err := memory.UpdatePatrolSchedule(ctx, schedule); err != nil {
		t.Fatalf("update schedule: %v", err)
	}
	if err := memory.UpdatePatrolSchedule(ctx, model.PatrolSchedule{ID: "psc_missing"}); !store.IsNotFound(err) {
		t.Fatalf("update missing schedule: %v", err)
	}
	if err := memory.DeletePatrolSchedule(ctx, "psc_missing"); !store.IsNotFound(err) {
		t.Fatalf("delete missing schedule: %v", err)
	}
	if err := memory.DeletePatrolSchedule(ctx, "psc_1"); err != nil {
		t.Fatalf("delete schedule: %v", err)
	}
	if err := memory.DeletePatrolRoute(ctx, "patrol_1"); err != nil {
		t.Fatalf("delete route: %v", err)
	}
}

func TestPatrolExecutionsLifecycle(t *testing.T) {
	memory := newMemory(t)
	ctx := context.Background()
	now := time.Now().UTC()

	makeExecution := func(id, status string, createdAt time.Time) model.PatrolExecution {
		return model.PatrolExecution{
			ID: id, RouteID: "patrol_1", RouteName: "路线", Status: status,
			EventID: "evt_1", ScheduledAt: createdAt, CreatedAt: createdAt,
		}
	}
	pending := makeExecution("pex_1", "pending", now)
	running := makeExecution("pex_2", "running", now.Add(-time.Hour))
	done := makeExecution("pex_3", "done", now.Add(-2*time.Hour))
	for _, execution := range []model.PatrolExecution{pending, running, done} {
		if err := memory.CreatePatrolExecution(ctx, execution); err != nil {
			t.Fatalf("create execution %s: %v", execution.ID, err)
		}
	}

	if _, ok, err := memory.GetPatrolExecution(ctx, "pex_1"); err != nil || !ok {
		t.Fatalf("get execution: %v ok=%v", err, ok)
	}
	if _, ok, _ := memory.GetPatrolExecution(ctx, "pex_missing"); ok {
		t.Fatal("missing execution should report not found")
	}

	byStatus, err := memory.ListPatrolExecutions(ctx, model.PatrolExecutionFilter{Status: "running", Limit: 10})
	if err != nil || len(byStatus) != 1 || byStatus[0].ID != "pex_2" {
		t.Fatalf("filter by status: %v %+v", err, byStatus)
	}
	byEvent, err := memory.ListPatrolExecutions(ctx, model.PatrolExecutionFilter{EventID: "evt_1", Limit: 10})
	if err != nil || len(byEvent) != 3 {
		t.Fatalf("filter by event: %v (%d)", err, len(byEvent))
	}
	bySchedule, err := memory.ListPatrolExecutions(ctx, model.PatrolExecutionFilter{ScheduleID: "psc_1", Limit: 10})
	if err != nil || len(bySchedule) != 0 {
		t.Fatalf("filter by schedule: %v (%d)", err, len(bySchedule))
	}
	from := now.Add(-90 * time.Minute)
	to := now.Add(time.Minute)
	byRange, err := memory.ListPatrolExecutions(ctx, model.PatrolExecutionFilter{From: &from, To: &to, Limit: 10})
	if err != nil || len(byRange) != 2 {
		t.Fatalf("filter by range: %v (%d)", err, len(byRange))
	}
	limited, err := memory.ListPatrolExecutions(ctx, model.PatrolExecutionFilter{Limit: 1})
	if err != nil || len(limited) != 1 {
		t.Fatalf("limit: %v (%d)", err, len(limited))
	}

	running.Status = "done"
	finished := now
	running.FinishedAt = &finished
	if err := memory.UpdatePatrolExecution(ctx, running); err != nil {
		t.Fatalf("update execution: %v", err)
	}
	if err := memory.UpdatePatrolExecution(ctx, model.PatrolExecution{ID: "pex_missing"}); !store.IsNotFound(err) {
		t.Fatalf("update missing execution: %v", err)
	}

	deleted, err := memory.DeletePatrolExecutions(ctx, now.Add(-time.Hour), []string{"done"})
	if err != nil || deleted != 2 {
		t.Fatalf("delete done executions: %v (%d)", err, deleted)
	}
	if _, ok, _ := memory.GetPatrolExecution(ctx, "pex_3"); ok {
		t.Fatal("done execution should be purged")
	}
	if _, ok, _ := memory.GetPatrolExecution(ctx, "pex_1"); !ok {
		t.Fatal("pending execution must survive a done purge")
	}
}

func TestEventWorkflowAndEscalation(t *testing.T) {
	memory := newMemory(t)
	ctx := context.Background()
	now := time.Now().UTC()

	event := model.Event{
		ID: "evt_wf_1", CameraID: "cam_001", CameraCode: "CAM-001", CameraName: "东门入口",
		Type: "intrusion", Severity: "warn", Confidence: 0.9, OccurredAt: now.Add(-time.Minute), Status: "new",
	}
	closedEvent := model.Event{
		ID: "evt_wf_2", CameraID: "cam_001", CameraCode: "CAM-001", CameraName: "东门入口",
		Type: "intrusion", Severity: "warn", Confidence: 0.9, OccurredAt: now.Add(-2 * time.Minute), Status: "closed",
	}
	infoEvent := model.Event{
		ID: "evt_wf_3", CameraID: "cam_002", CameraCode: "CAM-002", CameraName: "1号楼北侧",
		Type: "person", Severity: "info", Confidence: 0.9, OccurredAt: now.Add(-3 * time.Minute), Status: "new",
	}
	if err := memory.CreateEvents(ctx, []model.Event{event, closedEvent, infoEvent}); err != nil {
		t.Fatalf("create events: %v", err)
	}

	acked, err := memory.AckEvent(ctx, "evt_wf_1")
	if err != nil || acked.Status != "acked" {
		t.Fatalf("ack: %v %+v", err, acked)
	}
	if _, err := memory.AckEvent(ctx, "evt_missing"); !store.IsNotFound(err) {
		t.Fatalf("ack missing: %v", err)
	}
	closed, err := memory.CloseEvent(ctx, "evt_wf_1")
	if err != nil || closed.Status != "closed" || closed.ClosedAt == nil {
		t.Fatalf("close: %v %+v", err, closed)
	}
	if _, err := memory.CloseEvent(ctx, "evt_missing"); !store.IsNotFound(err) {
		t.Fatalf("close missing: %v", err)
	}

	// Closed events never escalate; open warn events below the cutoff do.
	closedPass, err := memory.EscalateStale(ctx, time.Time{}, []string{"warn", "critical"})
	if err != nil || len(closedPass) != 0 {
		t.Fatalf("early cutoff rotated nothing: %v (%d)", err, len(closedPass))
	}
	// evt_wf_1 was closed above; reopen it via AckEvent to model an active alarm.
	reopened, err := memory.AckEvent(ctx, "evt_wf_1")
	if err != nil || reopened.Status != "acked" {
		t.Fatalf("reopen: %v %+v", err, reopened)
	}
	escalated, err := memory.EscalateStale(ctx, now.Add(time.Second), []string{"warn", "critical"})
	if err != nil || len(escalated) != 1 || escalated[0].ID != "evt_wf_1" {
		t.Fatalf("unexpected escalation set: %v %+v", err, escalated)
	}
	// Already escalated events are skipped on a second pass.
	if again, err := memory.EscalateStale(ctx, now.Add(2*time.Second), []string{"warn", "critical"}); err != nil || len(again) != 0 {
		t.Fatalf("second escalation pass: %v (%d)", err, len(again))
	}

	reEscalated, err := memory.ReEscalateStale(ctx, now.Add(time.Hour), []string{"warn", "critical"})
	if err != nil || len(reEscalated) != 1 || reEscalated[0].ReEscalatedAt == nil {
		t.Fatalf("unexpected re-escalation: %v %+v", err, reEscalated)
	}
	// Already re-escalated events are skipped.
	if again, err := memory.ReEscalateStale(ctx, now.Add(2*time.Hour), []string{"warn"}); err != nil || len(again) != 0 {
		t.Fatalf("second re-escalation pass: %v (%d)", err, len(again))
	}
}

func TestCameraCalibrationAndSorting(t *testing.T) {
	memory := newMemory(t)
	ctx := context.Background()

	cameras, err := memory.ListCameras(ctx)
	if err != nil {
		t.Fatalf("list cameras: %v", err)
	}
	camera := cameras[0]
	calibration := &model.Calibration{Matrix: [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}, RMSE: 0.4, PointsCount: 5}
	updated, err := memory.SetCameraCalibration(ctx, camera.ID, calibration)
	if err != nil || updated.Calibration == nil || updated.Calibration.SolvedAt == nil {
		t.Fatalf("set calibration: %v %+v", err, updated)
	}
	cleared, err := memory.SetCameraCalibration(ctx, camera.ID, nil)
	if err != nil || cleared.Calibration != nil {
		t.Fatalf("clear calibration: %v %+v", err, cleared)
	}
	if _, err := memory.SetCameraCalibration(ctx, "cam_missing", calibration); !store.IsNotFound(err) {
		t.Fatalf("calibrate missing camera: %v", err)
	}

	codes := make([]string, 0, len(cameras))
	for _, item := range cameras {
		codes = append(codes, item.Code)
	}
	if !sortStringsAsc(codes) {
		t.Fatalf("ListCameras should be sorted by code: %v", codes)
	}

	accounts, err := memory.ListUserAccounts(ctx)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("fresh memory store should have no accounts, got %d", len(accounts))
	}
}

func sortStringsAsc(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] > values[index] {
			return false
		}
	}
	return true
}
