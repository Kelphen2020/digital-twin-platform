package calib

import (
	"math"
	"testing"

	"digital-twin-platform/api/internal/model"
)

func TestSolveRecoversAffineTransform(t *testing.T) {
	// Ground truth: x = 0.1*px - 100, y = 0.15*py - 50.
	points := []model.SolvePoint{
		{PX: 200, PY: 100}, {PX: 1600, PY: 120}, {PX: 1800, PY: 900},
		{PX: 300, PY: 1000}, {PX: 960, PY: 540}, {PX: 1200, PY: 300},
	}
	for i := range points {
		points[i].X = 0.1*points[i].PX - 100
		points[i].Y = 0.15*points[i].PY - 50
	}
	result, err := Solve(points)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if result.PointsCount != len(points) {
		t.Fatalf("points count = %d", result.PointsCount)
	}
	if result.RMSE > 1e-6 || result.MaxResidual > 1e-6 {
		t.Fatalf("residuals too high: rmse=%g max=%g", result.RMSE, result.MaxResidual)
	}
	matrix := result.Matrix
	checks := []struct {
		got, want float64
		name      string
	}{
		{matrix[0][0], 0.1, "h11"}, {matrix[0][1], 0, "h12"}, {matrix[0][2], -100, "h13"},
		{matrix[1][0], 0, "h21"}, {matrix[1][1], 0.15, "h22"}, {matrix[1][2], -50, "h23"},
		{matrix[2][0], 0, "h31"}, {matrix[2][1], 0, "h32"}, {matrix[2][2], 1, "h33"},
	}
	for _, check := range checks {
		if math.Abs(check.got-check.want) > 1e-6 {
			t.Fatalf("%s = %g, want %g", check.name, check.got, check.want)
		}
	}
}

func TestSolveRejectsTooFewOrCollinearPoints(t *testing.T) {
	if _, err := Solve([]model.SolvePoint{{PX: 0, PY: 0, X: 0, Y: 0}}); err == nil {
		t.Fatal("expected error for one point")
	}
	collinear := []model.SolvePoint{
		{PX: 100, PY: 100, X: 1, Y: 1},
		{PX: 200, PY: 200, X: 2, Y: 2},
		{PX: 300, PY: 300, X: 3, Y: 3},
		{PX: 400, PY: 400, X: 4, Y: 4},
	}
	if _, err := Solve(collinear); err == nil {
		t.Fatal("expected degenerate error for collinear points")
	}
}
