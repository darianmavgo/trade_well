package study

import (
	"fmt"
	"math"
)

type GrangerResult struct {
	Predictor  string  `db:"predictor"`
	Target     string  `db:"target"`
	LagMinutes int     `db:"lag_minutes"`
	PValue     float64 `db:"p_value"`
}

type VolatilityRow struct {
	Date       string  `db:"date"`
	VooVar15m  float64 `db:"voo_var_15m"`
	GldVar15m  float64 `db:"gld_var_15m"`
	UtenVar15m float64 `db:"uten_var_15m"`
}

// ComputeGrangerCausality tests whether predictor Granger-causes target up to maxLag.
// Matches statsmodels grangercausalitytests (ssr_ftest) implementation.
func ComputeGrangerCausality(predictorName, targetName string, predictor, target []float64, maxLag int) ([]GrangerResult, error) {
	if len(predictor) != len(target) {
		return nil, fmt.Errorf("predictor and target series must have equal length (%d vs %d)", len(predictor), len(target))
	}
	nTotal := len(predictor)
	if nTotal <= 3*maxLag+1 {
		return nil, fmt.Errorf("not enough observations for maxLag %d (got %d)", maxLag, nTotal)
	}

	results := make([]GrangerResult, 0, maxLag)

	for lag := 1; lag <= maxLag; lag++ {
		// Effective sample size for this lag
		n := nTotal - lag
		dfNum := float64(lag)
		dfDenom := float64(nTotal - 3*lag - 1)
		if dfDenom <= 0 {
			continue
		}

		// Build target Y vector: indices lag .. nTotal-1
		y := make([]float64, n)
		for i := 0; i < n; i++ {
			y[i] = target[lag+i]
		}

		// 1. Restricted regression: y_t = c + sum_{j=1}^lag alpha_j * y_{t-j}
		kRestricted := 1 + lag
		xR := make([][]float64, n)
		for i := 0; i < n; i++ {
			row := make([]float64, kRestricted)
			row[0] = 1.0 // Intercept
			tIdx := lag + i
			for j := 1; j <= lag; j++ {
				row[j] = target[tIdx-j]
			}
			xR[i] = row
		}

		ssrR, err := olsSSR(xR, y)
		if err != nil {
			return nil, fmt.Errorf("restricted OLS failed for lag %d: %w", lag, err)
		}

		// 2. Unrestricted regression: y_t = c + sum_{j=1}^lag alpha_j * y_{t-j} + sum_{j=1}^lag beta_j * x_{t-j}
		kUnrestricted := 1 + 2*lag
		xUR := make([][]float64, n)
		for i := 0; i < n; i++ {
			row := make([]float64, kUnrestricted)
			row[0] = 1.0 // Intercept
			tIdx := lag + i
			for j := 1; j <= lag; j++ {
				row[j] = target[tIdx-j]
			}
			for j := 1; j <= lag; j++ {
				row[lag+j] = predictor[tIdx-j]
			}
			xUR[i] = row
		}

		ssrUR, err := olsSSR(xUR, y)
		if err != nil {
			return nil, fmt.Errorf("unrestricted OLS failed for lag %d: %w", lag, err)
		}

		// F-statistic: ((SSR_r - SSR_ur) / df_num) / (SSR_ur / df_denom)
		diff := ssrR - ssrUR
		if diff < 0 {
			diff = 0
		}

		var fStat float64
		if ssrUR > 0 {
			fStat = (diff / dfNum) / (ssrUR / dfDenom)
		}

		pValue := fDistributionSurvival(fStat, dfNum, dfDenom)

		results = append(results, GrangerResult{
			Predictor:  predictorName,
			Target:     targetName,
			LagMinutes: lag,
			PValue:     pValue,
		})
	}

	return results, nil
}

// olsSSR computes the sum of squared residuals for OLS regression Y = X * beta + e
func olsSSR(X [][]float64, y []float64) (float64, error) {
	n := len(X)
	if n == 0 {
		return 0, fmt.Errorf("empty X matrix")
	}
	k := len(X[0])

	// Compute Normal Equations: (X^T * X) beta = X^T * y
	xtx := make([][]float64, k)
	for i := range xtx {
		xtx[i] = make([]float64, k)
	}
	xty := make([]float64, k)

	for i := 0; i < n; i++ {
		xi := X[i]
		yi := y[i]
		for j := 0; j < k; j++ {
			xij := xi[j]
			xty[j] += xij * yi
			for m := j; m < k; m++ {
				val := xij * xi[m]
				xtx[j][m] += val
				if j != m {
					xtx[m][j] += val
				}
			}
		}
	}

	beta, err := solveLinearSystem(xtx, xty)
	if err != nil {
		return 0, err
	}

	// Calculate SSR = sum (y_i - X_i * beta)^2
	var ssr float64
	for i := 0; i < n; i++ {
		xi := X[i]
		var yHat float64
		for j := 0; j < k; j++ {
			yHat += xi[j] * beta[j]
		}
		res := y[i] - yHat
		ssr += res * res
	}

	return ssr, nil
}

// solveLinearSystem solves A * x = b using Gaussian elimination with partial pivoting.
func solveLinearSystem(A [][]float64, b []float64) ([]float64, error) {
	n := len(A)
	// Create augmented matrix
	aug := make([][]float64, n)
	for i := range aug {
		aug[i] = make([]float64, n+1)
		copy(aug[i], A[i])
		aug[i][n] = b[i]
	}

	for col := 0; col < n; col++ {
		// Find pivot
		maxRow := col
		maxVal := math.Abs(aug[col][col])
		for row := col + 1; row < n; row++ {
			if v := math.Abs(aug[row][col]); v > maxVal {
				maxVal = v
				maxRow = row
			}
		}

		if maxVal < 1e-15 {
			return nil, fmt.Errorf("singular matrix near column %d", col)
		}

		// Swap rows
		if maxRow != col {
			aug[col], aug[maxRow] = aug[maxRow], aug[col]
		}

		pivot := aug[col][col]
		for row := col + 1; row < n; row++ {
			factor := aug[row][col] / pivot
			for j := col; j <= n; j++ {
				aug[row][j] -= factor * aug[col][j]
			}
		}
	}

	// Back substitution
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		sum := aug[i][n]
		for j := i + 1; j < n; j++ {
			sum -= aug[i][j] * x[j]
		}
		x[i] = sum / aug[i][i]
	}

	return x, nil
}

// fDistributionSurvival computes the p-value P(F >= fStat) for F(d1, d2).
func fDistributionSurvival(fStat, d1, d2 float64) float64 {
	if fStat <= 0 || math.IsNaN(fStat) {
		return 1.0
	}
	x := d2 / (d2 + d1*fStat)
	if x <= 0 {
		return 0.0
	}
	if x >= 1 {
		return 1.0
	}
	return regularizedBeta(x, d2/2.0, d1/2.0)
}

// regularizedBeta computes I_x(a, b), the regularized incomplete beta function,
// using continued fractions (Lentz's method).
func regularizedBeta(x, a, b float64) float64 {
	if x < 0 || x > 1 {
		return math.NaN()
	}
	if x == 0 {
		return 0
	}
	if x == 1 {
		return 1
	}

	// Factors front: exp(lgamma(a+b) - lgamma(a) - lgamma(b) + a*ln(x) + b*ln(1-x))
	logFront := math.Log(x)*a + math.Log(1.0-x)*b - logBeta(a, b)
	front := math.Exp(logFront)

	// Use symmetry if x > (a+1)/(a+b+2)
	if x > (a+1.0)/(a+b+2.0) {
		return 1.0 - front*betaContinuedFraction(1.0-x, b, a)/b
	}
	return front * betaContinuedFraction(x, a, b) / a
}

func logBeta(a, b float64) float64 {
	lga, _ := math.Lgamma(a)
	lgb, _ := math.Lgamma(b)
	lgab, _ := math.Lgamma(a + b)
	return lga + lgb - lgab
}

// betaContinuedFraction evaluates the continued fraction for incomplete beta using modified Lentz's method.
func betaContinuedFraction(x, a, b float64) float64 {
	const maxIterations = 200
	const epsilon = 1e-15
	const tiny = 1e-30

	c := 1.0
	d := 1.0 - (a+b)*x/(a+1.0)
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1.0 / d
	h := d

	for m := 1; m <= maxIterations; m++ {
		mF := float64(m)

		// Even step: d_{2m}
		numEven := mF * (b - mF) * x / ((a + 2.0*mF - 1.0) * (a + 2.0*mF))
		d = 1.0 + numEven*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1.0 + numEven/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1.0 / d
		h *= d * c

		// Odd step: d_{2m+1}
		numOdd := -(a + mF) * (a + b + mF) * x / ((a + 2.0*mF) * (a + 2.0*mF + 1.0))
		d = 1.0 + numOdd*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1.0 + numOdd/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1.0 / d
		delta := d * c
		h *= delta

		if math.Abs(delta-1.0) < epsilon {
			break
		}
	}
	return h
}

// ComputeRollingVariance computes rolling sample variance with window w (ddof = 1).
// Output has length len(values) - window + 1.
func ComputeRollingVariance(values []float64, window int) []float64 {
	if len(values) < window || window <= 1 {
		return nil
	}
	outLen := len(values) - window + 1
	result := make([]float64, outLen)

	wF := float64(window)
	degFreedom := float64(window - 1)

	for i := 0; i < outLen; i++ {
		win := values[i : i+window]
		var sum float64
		for _, v := range win {
			sum += v
		}
		mean := sum / wF

		var sqDiff float64
		for _, v := range win {
			diff := v - mean
			sqDiff += diff * diff
		}
		result[i] = sqDiff / degFreedom
	}

	return result
}

// ComputeCorrelation computes the Pearson correlation coefficient between x and y.
func ComputeCorrelation(x, y []float64) float64 {
	n := len(x)
	if n != len(y) || n < 2 {
		return 0.0
	}

	var sumX, sumY float64
	for i := 0; i < n; i++ {
		sumX += x[i]
		sumY += y[i]
	}
	meanX := sumX / float64(n)
	meanY := sumY / float64(n)

	var num, denX, denY float64
	for i := 0; i < n; i++ {
		dx := x[i] - meanX
		dy := y[i] - meanY
		num += dx * dy
		denX += dx * dx
		denY += dy * dy
	}

	den := math.Sqrt(denX * denY)
	if den <= 0 {
		return 0.0
	}
	return num / den
}
