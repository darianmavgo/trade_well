package study

import (
	"math/rand"
	"testing"
)

func TestHMMMath(t *testing.T) {
	// Generate a simple synthetic series
	obs := make([]float64, 100)
	for i := 0; i < 50; i++ {
		// "Bull" market, positive mean, low var
		obs[i] = rand.NormFloat64()*0.005 + 0.01
	}
	for i := 50; i < 100; i++ {
		// "Bear" market, negative mean, high var
		obs[i] = rand.NormFloat64()*0.015 - 0.01
	}

	hmm := &HMM{
		N:  3,
		Pi: []float64{0.33, 0.33, 0.34},
		A: [][]float64{
			{0.8, 0.1, 0.1},
			{0.1, 0.8, 0.1},
			{0.1, 0.1, 0.8},
		},
		Mu:    []float64{-0.01, 0.0, 0.01},
		Sigma: []float64{0.02, 0.01, 0.01},
	}

	hmm.Fit(obs, 20)

	if len(hmm.Mu) != 3 || len(hmm.Sigma) != 3 {
		t.Fatalf("Expected N=3, got length %d", len(hmm.Mu))
	}

	path := hmm.Viterbi(obs)
	if len(path) != len(obs) {
		t.Fatalf("Expected path length %d, got %d", len(obs), len(path))
	}
}
