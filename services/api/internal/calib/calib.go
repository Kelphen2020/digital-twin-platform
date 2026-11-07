// Package calib solves the pixel → world homography used by P1 calibration.
// It minimizes the algebraic error of the four-point DLT with h33 fixed to 1,
// which is accurate enough for fixed cameras and cheap to run per camera.
package calib

import (
	"errors"
	"fmt"
	"math"

	"digital-twin-platform/api/internal/model"
)

// ErrDegenerate means the control points cannot determine a homography.
var ErrDegenerate = errors.New("calibration points are degenerate")

// MinPoints is the minimum number of control points.
const MinPoints = 4

// MaxPoints bounds the request size.
const MaxPoints = 20

// Solve computes the homography mapping pixel coordinates to world ENU metres.
func Solve(points []model.SolvePoint) (model.SolveResult, error) {
	if len(points) < MinPoints {
		return model.SolveResult{}, fmt.Errorf("at least %d control points are required", MinPoints)
	}
	if len(points) > MaxPoints {
		return model.SolveResult{}, fmt.Errorf("at most %d control points are accepted", MaxPoints)
	}

	// Build the normal equations AᵀA h = Aᵀb for the eight unknowns
	// [h11 h12 h13 h21 h22 h23 h31 h32].
	var ata [8][8]float64
	var atb [8]float64
	for _, point := range points {
		rowX := [8]float64{point.PX, point.PY, 1, 0, 0, 0, -point.PX * point.X, -point.PY * point.X}
		rowY := [8]float64{0, 0, 0, point.PX, point.PY, 1, -point.PX * point.Y, -point.PY * point.Y}
		accumulate(&ata, &atb, rowX, point.X)
		accumulate(&ata, &atb, rowY, point.Y)
	}

	solution, ok := solveLinear(ata, atb)
	if !ok {
		return model.SolveResult{}, ErrDegenerate
	}

	result := model.SolveResult{
		Matrix: [3][3]float64{
			{solution[0], solution[1], solution[2]},
			{solution[3], solution[4], solution[5]},
			{solution[6], solution[7], 1},
		},
		PointsCount: len(points),
	}

	var sumSquares, maxResidual float64
	for _, point := range points {
		wx, wy, ok := Project(result.Matrix, point.PX, point.PY)
		if !ok {
			return model.SolveResult{}, ErrDegenerate
		}
		dx := wx - point.X
		dy := wy - point.Y
		residual := math.Hypot(dx, dy)
		sumSquares += residual * residual
		if residual > maxResidual {
			maxResidual = residual
		}
	}
	result.RMSE = math.Sqrt(sumSquares / float64(len(points)))
	result.MaxResidual = maxResidual
	if math.IsNaN(result.RMSE) || math.IsInf(result.RMSE, 0) {
		return model.SolveResult{}, ErrDegenerate
	}
	return result, nil
}

// Project maps one pixel to world coordinates through a homography.
func Project(matrix [3][3]float64, px, py float64) (float64, float64, bool) {
	w := matrix[2][0]*px + matrix[2][1]*py + matrix[2][2]
	if math.Abs(w) < 1e-9 {
		return 0, 0, false
	}
	x := (matrix[0][0]*px + matrix[0][1]*py + matrix[0][2]) / w
	y := (matrix[1][0]*px + matrix[1][1]*py + matrix[1][2]) / w
	if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
		return 0, 0, false
	}
	return x, y, true
}

func accumulate(ata *[8][8]float64, atb *[8]float64, row [8]float64, target float64) {
	for i := 0; i < 8; i++ {
		atb[i] += row[i] * target
		for j := 0; j < 8; j++ {
			ata[i][j] += row[i] * row[j]
		}
	}
}

// solveLinear solves a small dense system by Gauss-Jordan with partial pivoting.
func solveLinear(a [8][8]float64, b [8]float64) ([8]float64, bool) {
	for col := 0; col < 8; col++ {
		pivot := col
		for row := col + 1; row < 8; row++ {
			if math.Abs(a[row][col]) > math.Abs(a[pivot][col]) {
				pivot = row
			}
		}
		if math.Abs(a[pivot][col]) < 1e-12 {
			return [8]float64{}, false
		}
		a[col], a[pivot] = a[pivot], a[col]
		b[col], b[pivot] = b[pivot], b[col]
		divisor := a[col][col]
		for j := col; j < 8; j++ {
			a[col][j] /= divisor
		}
		b[col] /= divisor
		for row := 0; row < 8; row++ {
			if row == col {
				continue
			}
			factor := a[row][col]
			if factor == 0 {
				continue
			}
			for j := col; j < 8; j++ {
				a[row][j] -= factor * a[col][j]
			}
			b[row] -= factor * b[col]
		}
	}
	return b, true
}
