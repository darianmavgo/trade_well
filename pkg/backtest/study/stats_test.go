package study

import (
	"math"
	"testing"
)

func TestComputeRollingVariance(t *testing.T) {
	// Simple series: [1, 2, 3, 4, 5]
	// Window 3:
	// [1, 2, 3] -> mean = 2, sqDiff = (1-2)^2 + (2-2)^2 + (3-2)^2 = 2, var = 2 / 2 = 1.0
	// [2, 3, 4] -> mean = 3, sqDiff = 2, var = 1.0
	// [3, 4, 5] -> mean = 4, sqDiff = 2, var = 1.0
	data := []float64{1, 2, 3, 4, 5}
	vars := ComputeRollingVariance(data, 3)
	if len(vars) != 3 {
		t.Fatalf("expected 3 variances, got %d", len(vars))
	}
	for i, v := range vars {
		if math.Abs(v-1.0) > 1e-10 {
			t.Errorf("at index %d: expected 1.0, got %f", i, v)
		}
	}
}

func TestComputeCorrelation(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5}
	y := []float64{2, 4, 6, 8, 10}
	r := ComputeCorrelation(x, y)
	if math.Abs(r-1.0) > 1e-10 {
		t.Errorf("expected correlation 1.0, got %f", r)
	}

	z := []float64{5, 4, 3, 2, 1}
	rz := ComputeCorrelation(x, z)
	if math.Abs(rz-(-1.0)) > 1e-10 {
		t.Errorf("expected correlation -1.0, got %f", rz)
	}
}

func TestComputeGrangerCausality(t *testing.T) {
	// Create synthetic series where X causes Y: Y_t = 0.5 * Y_{t-1} + 0.8 * X_{t-1} + noise
	n := 200
	x := make([]float64, n)
	y := make([]float64, n)

	for i := 0; i < n; i++ {
		// deterministic pseudorandom pattern
		x[i] = math.Sin(float64(i) * 0.3)
	}
	for i := 1; i < n; i++ {
		y[i] = 0.5*y[i-1] + 0.8*x[i-1] + 0.05*math.Cos(float64(i)*0.7)
	}

	results, err := ComputeGrangerCausality("X", "Y", x, y, 3)
	if err != nil {
		t.Fatalf("ComputeGrangerCausality error: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 lag results, got %d", len(results))
	}

	// Lag 1 should be highly significant (p-value close to 0)
	if results[0].PValue > 0.01 {
		t.Errorf("lag 1 p-value expected < 0.01, got %f", results[0].PValue)
	}
	if results[0].LagMinutes != 1 {
		t.Errorf("expected lag 1, got %d", results[0].LagMinutes)
	}
}
