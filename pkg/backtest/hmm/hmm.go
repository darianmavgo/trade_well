// Package hmm is a Gaussian hidden Markov model: Baum-Welch fit and causal (forward-filtered)
// regime probabilities. It is the iterative-EM exception to "SQL does the calculation": the
// inputs are staged by SQL and the results are written to named tables by the caller.
//
// Live code uses Filter, never a smoothed or Viterbi path: the filtered probability for a day
// depends only on observations up to that day, so a regime shown for today is the one that was
// knowable today. States are sorted by the mean of the first observation dimension (the
// return), so state 0 is the lowest-return (bear) regime and N-1 the highest (bull).
package hmm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// Model holds the parameters of an N-state Gaussian HMM over D-dimensional observations.
type Model struct {
	N, D int
	Pi   []float64     // initial state probabilities, length N
	A    [][]float64   // A[i][j] = P(next state j | state i)
	Mu   [][]float64   // Mu[i] = mean vector of state i, length D
	Cov  [][][]float64 // Cov[i] = D x D covariance of state i
}

// Stats describes a fit.
type Stats struct {
	Bars    int
	LogLik  float64
	BIC     float64
	Iters   int
	Restart int // which restart won
}

// Options tunes Fit. Zero values take the defaults noted on each field.
type Options struct {
	Iters        int     // EM iterations per restart (default 200)
	Restarts     int     // random restarts besides the quantile start (default 4)
	Tol          float64 // stop when log-likelihood gains less than this (default 1e-6)
	Seed         int64   // restart RNG seed; the same input and seed always give the same model
	MinOccupancy float64 // smallest share of bars a state must own, else ErrDegenerate (default 0.02)
}

// ErrDegenerate means a state ended up owning almost no bars, so the fit says nothing about it.
var ErrDegenerate = errors.New("hmm: a state owns too few bars; use fewer states or more history")

// covFloor keeps every covariance matrix invertible when a state collapses onto a point.
const covFloor = 1e-9

func (o Options) withDefaults() Options {
	if o.Iters <= 0 {
		o.Iters = 200
	}
	if o.Restarts < 0 {
		o.Restarts = 0
	} else if o.Restarts == 0 {
		o.Restarts = 4
	}
	if o.Tol <= 0 {
		o.Tol = 1e-6
	}
	if o.MinOccupancy <= 0 {
		o.MinOccupancy = 0.02
	}
	return o
}

// NumParams is the free-parameter count used by BIC.
func NumParams(n, d int) int {
	return (n - 1) + n*(n-1) + n*d + n*d*(d+1)/2
}

// Fit trains an n-state model on obs (T rows of D columns) with Baum-Welch and returns the best
// of the quantile start and Options.Restarts random starts by log-likelihood.
func Fit(ctx context.Context, obs [][]float64, n int, opt Options) (*Model, Stats, error) {
	opt = opt.withDefaults()
	if n < 1 {
		return nil, Stats{}, fmt.Errorf("hmm: need at least 1 state, got %d", n)
	}
	if len(obs) == 0 {
		return nil, Stats{}, errors.New("hmm: no observations")
	}
	d := len(obs[0])
	for t, row := range obs {
		if len(row) != d {
			return nil, Stats{}, fmt.Errorf("hmm: observation %d has %d columns, want %d", t, len(row), d)
		}
		for _, v := range row {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, Stats{}, fmt.Errorf("hmm: observation %d is not finite", t)
			}
		}
	}
	if min := 10 * NumParams(n, d); len(obs) < min {
		return nil, Stats{}, fmt.Errorf("hmm: %d bars is too few for %d states of %d dimensions (need at least %d)", len(obs), n, d, min)
	}

	rng := rand.New(rand.NewSource(opt.Seed))
	var best *Model
	var bestStats Stats
	for r := 0; r <= opt.Restarts; r++ {
		if err := ctx.Err(); err != nil {
			return nil, Stats{}, err
		}
		var m *Model
		if r == 0 {
			m = quantileStart(obs, n)
		} else {
			m = randomStart(obs, n, rng)
		}
		ll, iters, err := baumWelch(ctx, m, obs, opt)
		if err != nil {
			return nil, Stats{}, err
		}
		if best == nil || ll > bestStats.LogLik {
			best, bestStats = m, Stats{Bars: len(obs), LogLik: ll, Iters: iters, Restart: r}
		}
	}

	occ := occupancy(best, obs)
	for _, share := range occ {
		if share < opt.MinOccupancy {
			return nil, Stats{}, ErrDegenerate
		}
	}
	best.sortByReturn()
	bestStats.BIC = -2*bestStats.LogLik + float64(NumParams(n, d))*math.Log(float64(len(obs)))
	return best, bestStats, nil
}

// Filtered is the causal output of Filter.
type Filtered struct {
	Probs     [][]float64 // Probs[t][i] = P(state i at t | obs[0..t])
	Next      [][]float64 // Next[t][i] = P(state i at t+1 | obs[0..t])
	LogLik    float64
	LogLikBar []float64 // LogLikBar[t] = ln P(obs[t] | obs[0..t-1]); sums to LogLik
}

// Filter runs the forward algorithm with the model's frozen parameters. Row t uses only
// obs[0..t], so appending later observations never changes an earlier row.
func (m *Model) Filter(obs [][]float64) (Filtered, error) {
	if err := m.validate(); err != nil {
		return Filtered{}, err
	}
	T := len(obs)
	f := Filtered{Probs: make([][]float64, T), Next: make([][]float64, T), LogLikBar: make([]float64, T)}
	em := m.emitters()
	prev := m.Pi
	for t := 0; t < T; t++ {
		if len(obs[t]) != m.D {
			return Filtered{}, fmt.Errorf("hmm: observation %d has %d columns, want %d", t, len(obs[t]), m.D)
		}
		b, shift := em.density(obs[t])
		a := make([]float64, m.N)
		sum := 0.0
		for i := 0; i < m.N; i++ {
			p := prev[i]
			if t > 0 {
				p = 0
				for j := 0; j < m.N; j++ {
					p += f.Probs[t-1][j] * m.A[j][i]
				}
			}
			a[i] = p * b[i]
			sum += a[i]
		}
		if sum <= 0 {
			return Filtered{}, fmt.Errorf("hmm: observation %d has zero likelihood under every state", t)
		}
		for i := range a {
			a[i] /= sum
		}
		f.LogLikBar[t] = math.Log(sum) + shift
		f.LogLik += f.LogLikBar[t]
		f.Probs[t] = a
		nx := make([]float64, m.N)
		for i := 0; i < m.N; i++ {
			for j := 0; j < m.N; j++ {
				nx[j] += a[i] * m.A[i][j]
			}
		}
		f.Next[t] = nx
	}
	return f, nil
}

// Argmax returns the most probable state of a probability row.
func Argmax(p []float64) int {
	best := 0
	for i, v := range p {
		if v > p[best] {
			best = i
		}
	}
	return best
}

func (m *Model) validate() error {
	if m == nil || m.N < 1 || m.D < 1 {
		return errors.New("hmm: empty model")
	}
	if len(m.Pi) != m.N || len(m.A) != m.N || len(m.Mu) != m.N || len(m.Cov) != m.N {
		return errors.New("hmm: model dimensions are inconsistent")
	}
	for i := 0; i < m.N; i++ {
		if len(m.A[i]) != m.N || len(m.Mu[i]) != m.D || len(m.Cov[i]) != m.D {
			return errors.New("hmm: model dimensions are inconsistent")
		}
	}
	return nil
}

// sortByReturn reorders the states by ascending mean of dimension 0.
func (m *Model) sortByReturn() {
	idx := make([]int, m.N)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return m.Mu[idx[a]][0] < m.Mu[idx[b]][0] })
	pi := make([]float64, m.N)
	a := make([][]float64, m.N)
	mu := make([][]float64, m.N)
	cov := make([][][]float64, m.N)
	for newI, oldI := range idx {
		pi[newI] = m.Pi[oldI]
		mu[newI] = m.Mu[oldI]
		cov[newI] = m.Cov[oldI]
		a[newI] = make([]float64, m.N)
		for newJ, oldJ := range idx {
			a[newI][newJ] = m.A[oldI][oldJ]
		}
	}
	m.Pi, m.A, m.Mu, m.Cov = pi, a, mu, cov
}

func stickyA(n int) [][]float64 {
	a := make([][]float64, n)
	for i := range a {
		a[i] = make([]float64, n)
		for j := range a[i] {
			if i == j {
				a[i][j] = 0.9
			} else if n > 1 {
				a[i][j] = 0.1 / float64(n-1)
			}
		}
		if n == 1 {
			a[i][i] = 1
		}
	}
	return a
}

func uniform(n int) []float64 {
	p := make([]float64, n)
	for i := range p {
		p[i] = 1 / float64(n)
	}
	return p
}

// quantileStart splits the bars into n equal groups by the first dimension (the return) and
// takes each group's mean and covariance. It is deterministic.
func quantileStart(obs [][]float64, n int) *Model {
	d := len(obs[0])
	order := make([]int, len(obs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return obs[order[a]][0] < obs[order[b]][0] })
	m := &Model{N: n, D: d, Pi: uniform(n), A: stickyA(n), Mu: make([][]float64, n), Cov: make([][][]float64, n)}
	for i := 0; i < n; i++ {
		lo, hi := i*len(obs)/n, (i+1)*len(obs)/n
		rows := make([][]float64, 0, hi-lo)
		for _, k := range order[lo:hi] {
			rows = append(rows, obs[k])
		}
		m.Mu[i], m.Cov[i] = meanCov(rows)
	}
	return m
}

// randomStart centres each state on a random bar with the whole sample's covariance.
func randomStart(obs [][]float64, n int, rng *rand.Rand) *Model {
	d := len(obs[0])
	_, cov := meanCov(obs)
	m := &Model{N: n, D: d, Pi: uniform(n), A: stickyA(n), Mu: make([][]float64, n), Cov: make([][][]float64, n)}
	for i := 0; i < n; i++ {
		m.Mu[i] = append([]float64(nil), obs[rng.Intn(len(obs))]...)
		m.Cov[i] = cloneMat(cov)
	}
	return m
}

func meanCov(rows [][]float64) ([]float64, [][]float64) {
	d := len(rows[0])
	mu := make([]float64, d)
	for _, r := range rows {
		for k, v := range r {
			mu[k] += v
		}
	}
	for k := range mu {
		mu[k] /= float64(len(rows))
	}
	cov := newMat(d)
	for _, r := range rows {
		for a := 0; a < d; a++ {
			for b := 0; b < d; b++ {
				cov[a][b] += (r[a] - mu[a]) * (r[b] - mu[b])
			}
		}
	}
	for a := 0; a < d; a++ {
		for b := 0; b < d; b++ {
			cov[a][b] /= float64(len(rows))
		}
		cov[a][a] += covFloor
	}
	return mu, cov
}

func newMat(d int) [][]float64 {
	m := make([][]float64, d)
	for i := range m {
		m[i] = make([]float64, d)
	}
	return m
}

func cloneMat(src [][]float64) [][]float64 {
	m := newMat(len(src))
	for i := range src {
		copy(m[i], src[i])
	}
	return m
}

// emitters caches the Cholesky factor and log-determinant of each state's covariance.
type emitters struct {
	m      *Model
	chol   [][][]float64
	logDet []float64
}

func (m *Model) emitters() *emitters {
	e := &emitters{m: m, chol: make([][][]float64, m.N), logDet: make([]float64, m.N)}
	for i := 0; i < m.N; i++ {
		l := cholesky(m.Cov[i])
		e.chol[i] = l
		ld := 0.0
		for k := 0; k < m.D; k++ {
			ld += 2 * math.Log(l[k][k])
		}
		e.logDet[i] = ld
	}
	return e
}

// cholesky returns the lower factor of a symmetric matrix, adding the floor to a diagonal
// entry that would otherwise go non-positive.
func cholesky(a [][]float64) [][]float64 {
	d := len(a)
	l := newMat(d)
	for i := 0; i < d; i++ {
		for j := 0; j <= i; j++ {
			s := a[i][j]
			for k := 0; k < j; k++ {
				s -= l[i][k] * l[j][k]
			}
			if i == j {
				if s < covFloor {
					s = covFloor
				}
				l[i][i] = math.Sqrt(s)
			} else {
				l[i][j] = s / l[j][j]
			}
		}
	}
	return l
}

// density returns each state's likelihood of x, scaled by exp(-shift) so the largest log
// density becomes 1 (avoids underflow); shift is added back when summing the log-likelihood.
func (e *emitters) density(x []float64) ([]float64, float64) {
	n, d := e.m.N, e.m.D
	logp := make([]float64, n)
	shift := math.Inf(-1)
	z := make([]float64, d)
	for i := 0; i < n; i++ {
		l := e.chol[i]
		q := 0.0
		for r := 0; r < d; r++ {
			s := x[r] - e.m.Mu[i][r]
			for k := 0; k < r; k++ {
				s -= l[r][k] * z[k]
			}
			z[r] = s / l[r][r]
			q += z[r] * z[r]
		}
		logp[i] = -0.5 * (float64(d)*math.Log(2*math.Pi) + e.logDet[i] + q)
		if logp[i] > shift {
			shift = logp[i]
		}
	}
	b := make([]float64, n)
	for i := range b {
		b[i] = math.Exp(logp[i] - shift)
	}
	return b, shift
}

// baumWelch runs EM on m in place and returns the final log-likelihood.
func baumWelch(ctx context.Context, m *Model, obs [][]float64, opt Options) (float64, int, error) {
	T, N, D := len(obs), m.N, m.D
	prevLL := math.Inf(-1)
	ll := prevLL
	iter := 0
	for ; iter < opt.Iters; iter++ {
		if err := ctx.Err(); err != nil {
			return 0, iter, err
		}
		em := m.emitters()
		b := make([][]float64, T)
		alpha := make([][]float64, T)
		c := make([]float64, T)
		ll = 0
		for t := 0; t < T; t++ {
			bt, shift := em.density(obs[t])
			b[t] = bt
			a := make([]float64, N)
			sum := 0.0
			for i := 0; i < N; i++ {
				p := m.Pi[i]
				if t > 0 {
					p = 0
					for j := 0; j < N; j++ {
						p += alpha[t-1][j] * m.A[j][i]
					}
				}
				a[i] = p * bt[i]
				sum += a[i]
			}
			if sum <= 0 {
				return 0, iter, ErrDegenerate
			}
			for i := range a {
				a[i] /= sum
			}
			c[t] = sum
			alpha[t] = a
			ll += math.Log(sum) + shift
		}
		beta := make([][]float64, T)
		beta[T-1] = make([]float64, N)
		for i := range beta[T-1] {
			beta[T-1][i] = 1
		}
		for t := T - 2; t >= 0; t-- {
			beta[t] = make([]float64, N)
			for i := 0; i < N; i++ {
				s := 0.0
				for j := 0; j < N; j++ {
					s += m.A[i][j] * b[t+1][j] * beta[t+1][j]
				}
				beta[t][i] = s / c[t+1]
			}
		}

		gamma := make([][]float64, T)
		xi := newMat(N)
		for t := 0; t < T; t++ {
			g := make([]float64, N)
			gs := 0.0
			for i := 0; i < N; i++ {
				g[i] = alpha[t][i] * beta[t][i]
				gs += g[i]
			}
			for i := range g {
				g[i] /= gs
			}
			gamma[t] = g
			if t < T-1 {
				for i := 0; i < N; i++ {
					for j := 0; j < N; j++ {
						xi[i][j] += alpha[t][i] * m.A[i][j] * b[t+1][j] * beta[t+1][j] / c[t+1]
					}
				}
			}
		}

		copy(m.Pi, gamma[0])
		for i := 0; i < N; i++ {
			rowSum := 0.0
			for j := 0; j < N; j++ {
				rowSum += xi[i][j]
			}
			if rowSum > 0 {
				for j := 0; j < N; j++ {
					m.A[i][j] = xi[i][j] / rowSum
				}
			}
			w := 0.0
			mu := make([]float64, D)
			for t := 0; t < T; t++ {
				w += gamma[t][i]
				for k := 0; k < D; k++ {
					mu[k] += gamma[t][i] * obs[t][k]
				}
			}
			if w <= 0 {
				return 0, iter, ErrDegenerate
			}
			for k := range mu {
				mu[k] /= w
			}
			cov := newMat(D)
			for t := 0; t < T; t++ {
				for r := 0; r < D; r++ {
					for s := 0; s < D; s++ {
						cov[r][s] += gamma[t][i] * (obs[t][r] - mu[r]) * (obs[t][s] - mu[s])
					}
				}
			}
			for r := 0; r < D; r++ {
				for s := 0; s < D; s++ {
					cov[r][s] /= w
				}
				cov[r][r] += covFloor
			}
			m.Mu[i], m.Cov[i] = mu, cov
		}

		if ll-prevLL < opt.Tol && iter > 0 {
			iter++
			break
		}
		prevLL = ll
	}
	return ll, iter, nil
}

// occupancy is the share of bars each state owns, summing the filtered probabilities.
func occupancy(m *Model, obs [][]float64) []float64 {
	f, err := m.Filter(obs)
	out := make([]float64, m.N)
	if err != nil {
		return out
	}
	for _, p := range f.Probs {
		for i, v := range p {
			out[i] += v
		}
	}
	for i := range out {
		out[i] /= float64(len(obs))
	}
	return out
}
