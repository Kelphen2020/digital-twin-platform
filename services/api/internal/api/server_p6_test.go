package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSiteConfig(t *testing.T) {
	ts, _ := newP3Server(t)
	admin := login(t, ts, "admin", "Admin@123456")
	viewer := login(t, ts, "viewer", "Viewer@123")

	status, body := authorized(t, http.MethodGet, ts.URL+"/api/site", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("site: %d %s", status, body)
	}
	var site struct {
		Name   string `json:"name"`
		Extent struct {
			MinX float64 `json:"min_x"`
			MaxX float64 `json:"max_x"`
			MinY float64 `json:"min_y"`
			MaxY float64 `json:"max_y"`
		} `json:"extent"`
		Models []struct {
			ID     string `json:"id"`
			Format string `json:"format"`
			URL    string `json:"url"`
		} `json:"models"`
		Map struct {
			Style string `json:"style"`
		} `json:"map"`
	}
	if err := json.Unmarshal(body, &site); err != nil {
		t.Fatalf("decode site: %v", err)
	}
	if site.Name == "" || site.Extent.MaxX <= site.Extent.MinX || site.Extent.MaxY <= site.Extent.MinY {
		t.Fatalf("site extent invalid: %+v", site)
	}
	if len(site.Models) == 0 || site.Models[0].URL == "" || site.Models[0].Format != "gltf" {
		t.Fatalf("site models invalid: %s", body)
	}
	if site.Map.Style != "vector" {
		t.Fatalf("expected vector map style: %s", body)
	}

	// Any authenticated role may read the site.
	status, body = authorized(t, http.MethodGet, ts.URL+"/api/site", viewer, nil)
	if status != http.StatusOK || !strings.Contains(string(body), site.Name) {
		t.Fatalf("viewer site: %d %s", status, body)
	}
	// Authentication is required.
	status, _ = doJSON(t, http.MethodGet, ts.URL+"/api/site", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", status)
	}
}
