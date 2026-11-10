package model_test

import (
	"testing"

	"digital-twin-platform/api/internal/model"
)

func TestDefaults(t *testing.T) {
	escalation := model.DefaultEscalationSettings()
	if !escalation.Enabled || escalation.AfterSeconds != 300 || !escalation.AutoDispatch {
		t.Fatalf("escalation defaults: %+v", escalation)
	}
	if len(escalation.Severities) != 2 || escalation.SecondAfterSeconds != 0 || escalation.AutoPatrol {
		t.Fatalf("escalation defaults: %+v", escalation)
	}

	notifications := model.DefaultNotificationSettings()
	if notifications.Enabled || notifications.Format != "generic" || notifications.MinSeverity != "warn" {
		t.Fatalf("notification defaults: %+v", notifications)
	}
	if notifications.TimeoutSeconds <= 0 || notifications.MaxAttempts < 1 || notifications.BackoffSeconds < 1 || notifications.SilenceSeconds < 0 {
		t.Fatalf("notification tuning out of range: %+v", notifications)
	}

	rules := model.DefaultCameraRules("cam_001")
	if rules.CameraID != "cam_001" || rules.ConfidenceThreshold <= 0 || rules.DedupeSeconds <= 0 {
		t.Fatalf("camera rules defaults: %+v", rules)
	}

	site := model.DefaultSite()
	if site.Name == "" || len(site.Models) == 0 || site.Extent.MaxX <= site.Extent.MinX || site.Extent.MaxY <= site.Extent.MinY {
		t.Fatalf("site defaults: %+v", site)
	}
}

func TestSeverityRank(t *testing.T) {
	if model.SeverityRank("info") >= model.SeverityRank("warn") {
		t.Fatal("info should rank below warn")
	}
	if model.SeverityRank("warn") >= model.SeverityRank("critical") {
		t.Fatal("warn should rank below critical")
	}
	// Unknown severities rank below info (0) so they never outrank known ones.
	if model.SeverityRank("unknown") != 0 || model.SeverityRank("") != 0 {
		t.Fatalf("unknown severity should rank 0: %d/%d", model.SeverityRank("unknown"), model.SeverityRank(""))
	}
	if model.SeverityRank("info") != 1 || model.SeverityRank("warn") != 2 || model.SeverityRank("critical") != 3 {
		t.Fatal("known severity ranks changed")
	}
}
