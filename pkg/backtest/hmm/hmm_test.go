package hmm

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"testing"
)

// regimeSeries draws T two-dimensional observations (return, volatility) from three sticky
// regimes with known means, and returns the true state path.
func regimeSeries(T int, seed int64) ([][]float64, []int) {
	rng := rand.New(rand.NewSource(seed))
	mu := [][]float64{{-0.012, 0.030}, {0.000, 0.010}, {0.010, 0.006}}
	sd := [][]float64{{0.012, 0.006}, {0.004, 0.002}, {0.004, 0.002}}
	obs := make([][]float64, T)
	path := make([]int, T)
	s := 1
	for t := 0; t < T; t++ {
		if rng.Float64() > 0.97 {
			s = rng.Intn(3)
		}
		path[t] = s
		obs[t] = []float64{mu[s][0] + sd[s][0]*rng.NormFloat64(), math.Abs(mu[s][1] + sd[s][1]*rng.NormFloat64())}
	}
	return obs, path
}

func TestFitRecoversSortedRegimes(t *testing.T) {
	obs, _ := regimeSeries(3000, 7)
	m, st, err := Fit(context.Background(), obs, 3, Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < m.N; i++ {
		if m.Mu[i][0] <= m.Mu[i-1][0] {
			t.Fatalf("states not sorted by mean return: %v", m.Mu)
		}
	}
	if m.Mu[0][0] > -0.004 || m.Mu[2][0] < 0.004 {
		t.Fatalf("bear/bull means not recovered: %v", m.Mu)
	}
	if m.Mu[0][1] <= m.Mu[2][1] {
		t.Fatalf("bear regime should be the volatile one: %v", m.Mu)
	}
	for i := range m.A {
		sum := 0.0
		for _, v := range m.A[i] {
			sum += v
		}
		if math.Abs(sum-1) > 1e-9 {
			t.Fatalf("row %d of A sums to %v", i, sum)
		}
	}
	if st.Bars != 3000 || math.IsNaN(st.BIC) || st.BIC == 0 {
		t.Fatalf("bad stats: %+v", st)
	}
}

func TestFitIsDeterministicForASeed(t *testing.T) {
	obs, _ := regimeSeries(1500, 3)
	a, sa, err := Fit(context.Background(), obs, 3, Options{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	b, sb, err := Fit(context.Background(), obs, 3, Options{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	if sa.LogLik != sb.LogLik || a.Mu[0][0] != b.Mu[0][0] {
		t.Fatalf("same input and seed gave different models: %v vs %v", sa.LogLik, sb.LogLik)
	}
}

func TestFilterRowsAreProbabilities(t *testing.T) {
	obs, _ := regimeSeries(1500, 11)
	m, _, err := Fit(context.Background(), obs, 3, Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	f, err := m.Filter(obs)
	if err != nil {
		t.Fatal(err)
	}
	for ti := range f.Probs {
		for _, row := range [][]float64{f.Probs[ti], f.Next[ti]} {
			sum := 0.0
			for _, v := range row {
				if v < 0 || v > 1 {
					t.Fatalf("row %d has probability %v", ti, v)
				}
				sum += v
			}
			if math.Abs(sum-1) > 1e-9 {
				t.Fatalf("row %d sums to %v", ti, sum)
			}
		}
	}
}

// The filtered probability for a day must not depend on later days: the live job runs once a
// day and today's regime cannot change when tomorrow's bar arrives.
func TestFilterHasNoLookAhead(t *testing.T) {
	obs, _ := regimeSeries(1500, 13)
	m, _, err := Fit(context.Background(), obs[:1200], 3, Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	full, err := m.Filter(obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, cut := range []int{50, 400, 1199} {
		part, err := m.Filter(obs[:cut])
		if err != nil {
			t.Fatal(err)
		}
		for ti := 0; ti < cut; ti++ {
			for i := range part.Probs[ti] {
				if part.Probs[ti][i] != full.Probs[ti][i] || part.Next[ti][i] != full.Next[ti][i] {
					t.Fatalf("row %d changed when later bars were added (cut %d)", ti, cut)
				}
			}
		}
	}
}

func TestFilterTracksTrueRegimes(t *testing.T) {
	obs, path := regimeSeries(3000, 17)
	m, _, err := Fit(context.Background(), obs, 3, Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	f, err := m.Filter(obs)
	if err != nil {
		t.Fatal(err)
	}
	hit := 0
	for ti := range path {
		if Argmax(f.Probs[ti]) == path[ti] {
			hit++
		}
	}
	if share := float64(hit) / float64(len(path)); share < 0.6 {
		t.Fatalf("filtered state matches the true regime on only %.0f%% of bars", share*100)
	}
}

func TestFitRejectsThinHistory(t *testing.T) {
	obs, _ := regimeSeries(200, 1)
	if _, _, err := Fit(context.Background(), obs, 3, Options{}); err == nil {
		t.Fatal("200 bars should be too few for 3 two-dimensional states")
	}
}

func TestFitRejectsBadInput(t *testing.T) {
	if _, _, err := Fit(context.Background(), nil, 3, Options{}); err == nil {
		t.Fatal("no observations should be an error")
	}
	obs, _ := regimeSeries(1000, 1)
	obs[10][0] = math.NaN()
	if _, _, err := Fit(context.Background(), obs, 3, Options{}); err == nil {
		t.Fatal("a NaN observation should be an error")
	}
}

func TestFitHonoursCancelledContext(t *testing.T) {
	obs, _ := regimeSeries(1000, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Fit(ctx, obs, 3, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestFitRefusesAStateThatOwnsNothing(t *testing.T) {
	// One cluster of identical-scale noise cannot support six states.
	rng := rand.New(rand.NewSource(2))
	obs := make([][]float64, 3000)
	for i := range obs {
		obs[i] = []float64{0.001 * rng.NormFloat64(), 0.01}
	}
	_, _, err := Fit(context.Background(), obs, 6, Options{Seed: 1, MinOccupancy: 0.12})
	if !errors.Is(err, ErrDegenerate) {
		t.Fatalf("got %v, want ErrDegenerate", err)
	}
}
