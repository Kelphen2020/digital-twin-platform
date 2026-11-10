package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func doJSONWithHeaders(t *testing.T, method, url string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(raw))
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return response.StatusCode, payload
}

func TestCalibrationSolveSaveAndDelete(t *testing.T) {
	ts := newTestServer(t)
	points := []map[string]float64{
		{"px": 200, "py": 100, "x": -80, "y": -35},
		{"px": 1600, "py": 120, "x": 60, "y": -32},
		{"px": 1800, "py": 900, "x": 80, "y": 85},
		{"px": 300, "py": 1000, "x": -70, "y": 100},
		{"px": 960, "py": 540, "x": -4, "y": 31},
	}
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/calibration/solve", map[string]any{
		"camera_id": "cam_001", "points": points,
	})
	if status != http.StatusOK {
		t.Fatalf("solve: %d %s", status, body)
	}
	var solved struct {
		Matrix      [3][3]float64 `json:"matrix"`
		RMSE        float64       `json:"rmse"`
		PointsCount int           `json:"points_count"`
	}
	if err := json.Unmarshal(body, &solved); err != nil {
		t.Fatalf("decode solve: %v", err)
	}
	if solved.PointsCount != len(points) || solved.RMSE > 1 {
		t.Fatalf("unexpected solve result: %s", body)
	}

	status, body = doJSON(t, http.MethodPut, ts.URL+"/api/cameras/cam_001/calibration", map[string]any{
		"matrix": solved.Matrix, "rmse": solved.RMSE, "points_count": solved.PointsCount,
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"calibration":{`) {
		t.Fatalf("put calibration: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/cameras/cam_001", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"solved_at"`) {
		t.Fatalf("get camera calibration: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodDelete, ts.URL+"/api/cameras/cam_001/calibration", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"calibration":null`) {
		t.Fatalf("delete calibration: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodPost, ts.URL+"/api/calibration/solve", map[string]any{
		"camera_id": "cam_001",
		"points":    []map[string]float64{{"px": 1, "py": 1, "x": 1, "y": 1}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for too few points, got %d %s", status, body)
	}
}

func TestTracksAndIngestWithTrackID(t *testing.T) {
	ts := newTestServer(t)
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": "CAM-003",
		"detections": []map[string]any{{
			"class": "person", "confidence": 0.91, "bbox": []float64{0.4, 0.3, 0.6, 0.8},
			"track_id": "trk-test-1", "world": map[string]float64{"x": -70, "y": 25, "z": 0},
		}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("ingest: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/tracks?camera_id=cam_003&limit=10", nil)
	if status != http.StatusOK || !strings.Contains(string(body), "trk-test-1") {
		t.Fatalf("tracks: %d %s", status, body)
	}
}

func TestEventWorkflowAuditStatsCSV(t *testing.T) {
	ts := newTestServer(t)
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/ingest/detections", map[string]any{
		"camera_code": "CAM-005",
		"detections": []map[string]any{{
			"class": "intrusion", "confidence": 0.95, "bbox": []float64{0.4, 0.3, 0.6, 0.8},
			"world": map[string]float64{"x": 42, "y": -78, "z": 0},
		}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("ingest: %d %s", status, body)
	}
	var ingest struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &ingest); err != nil || len(ingest.Events) != 1 {
		t.Fatalf("decode ingest: %v %s", err, body)
	}
	eventID := ingest.Events[0].ID

	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/events/"+eventID, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"actions":[]`) {
		t.Fatalf("event detail: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodPost, ts.URL+"/api/events/"+eventID+"/dispatch", map[string]any{
		"target": "安保A组", "note": "5分钟内到场",
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"action":"dispatch"`) {
		t.Fatalf("dispatch: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodPost, ts.URL+"/api/events/"+eventID+"/close", map[string]any{"note": "已处置"})
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"closed"`) {
		t.Fatalf("close: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/events/"+eventID, nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"action":"dispatch"`) || !strings.Contains(string(body), `"action":"close"`) {
		t.Fatalf("event actions: %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/audit?limit=50", nil)
	if status != http.StatusOK || !strings.Contains(string(body), "event.dispatch") || !strings.Contains(string(body), "event.close") {
		t.Fatalf("audit: %d %s", status, body)
	}

	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/stats/summary", nil)
	if status != http.StatusOK {
		t.Fatalf("stats: %d %s", status, body)
	}
	var summary struct {
		Totals struct {
			Events int64 `json:"events"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(body, &summary); err != nil || summary.Totals.Events < 1 {
		t.Fatalf("stats totals: %v %s", err, body)
	}

	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/stats/events.csv", nil)
	if status != http.StatusOK || !strings.HasPrefix(string(body), "id,camera_code,camera_name,type,severity") {
		t.Fatalf("csv: %d %s", status, body)
	}
}

func TestEventNotFoundCodes(t *testing.T) {
	ts := newTestServer(t)
	status, body := doJSON(t, http.MethodPost, ts.URL+"/api/events/evt_missing/close", map[string]any{})
	if status != http.StatusNotFound || !strings.Contains(string(body), "event_not_found") {
		t.Fatalf("expected event_not_found, got %d %s", status, body)
	}
	status, body = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/cameras/nope", ts.URL), nil)
	if status != http.StatusNotFound || !strings.Contains(string(body), "camera_not_found") {
		t.Fatalf("expected camera_not_found, got %d %s", status, body)
	}
}
