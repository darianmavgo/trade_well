package study

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// QQQTQQQVolumeStudy examines how trading volume in QQQ (the Nasdaq-100 ETF)
// relates to volume in TQQQ (its 3x leveraged version) at daily frequency.
//
// Questions it answers, each in its own results table:
//   - how big is TQQQ volume relative to QQQ (shares and dollars), and how has that changed by year;
//   - do day-to-day volume changes move together (same-day correlation), and does either lead the other (lag correlation, Granger);
//   - is the link stable through time (rolling 60-day correlation);
//   - how much does TQQQ volume scale with QQQ volume (log-log elasticity);
//   - how does the TQQQ/QQQ volume mix shift with the size and direction of QQQ's daily move.
//
// Volumes come from the daily bars as stored (Yahoo split-adjusts share volume, so
// TQQQ's reverse-split history is consistent); dollar volume = close x volume.
type QQQTQQQVolumeStudy struct {
	marketDBPath  string
	resultsDBPath string
}

func init() { Register(&QQQTQQQVolumeStudy{}) }

func (s *QQQTQQQVolumeStudy) ID() string   { return "qqq_tqqq_volume" }
func (s *QQQTQQQVolumeStudy) Name() string { return "QQQ vs TQQQ Volume Relationship (daily)" }
func (s *QQQTQQQVolumeStudy) Description() string {
	return "Daily QQQ and TQQQ volume: TQQQ/QQQ volume ratio by year, same-day and lead/lag correlation of volume changes, " +
		"Granger tests, rolling correlation, log-log elasticity, and how the volume mix changes with QQQ's move size."
}
func (s *QQQTQQQVolumeStudy) SetDatabases(marketDBPath, resultsDBPath string) {
	s.marketDBPath, s.resultsDBPath = marketDBPath, resultsDBPath
}

type qtDay struct {
	Date      string  `db:"Date"`
	QClose    float64 `db:"qc"`
	QVol      float64 `db:"qv"`
	TClose    float64 `db:"tc"`
	TVol      float64 `db:"tv"`
	QRet      float64 // close-to-close, 0 on the first row
	TRet      float64
	DlogQ     float64 // ln(vol_t / vol_{t-1}); NaN on the first row
	DlogT     float64
	ShareRat  float64 // TQQQ shares / QQQ shares
	DollarRat float64 // TQQQ $ volume / QQQ $ volume
}

func (s *QQQTQQQVolumeStudy) Run() error {
	log.Printf("Running study: %s", s.Name())
	if err := os.MkdirAll(filepath.Dir(s.resultsDBPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}
	mdb, err := sqlx.Open("sqlite", s.marketDBPath)
	if err != nil {
		return fmt.Errorf("open market db: %w", err)
	}
	defer mdb.Close()

	var days []qtDay
	if err := mdb.Select(&days, `
		SELECT substr(q.Date,1,10) AS Date, q.close AS qc, q.volume AS qv, t.close AS tc, t.volume AS tv
		FROM backtest_start q
		JOIN backtest_start t ON substr(t.Date,1,10) = substr(q.Date,1,10) AND t.symbol = 'TQQQ' AND t.timeframe = '1d'
		WHERE q.symbol = 'QQQ' AND q.timeframe = '1d' AND length(q.Date) = 10 AND length(t.Date) = 10
		  AND q.volume > 0 AND t.volume > 0 AND q.close > 0 AND t.close > 0
		ORDER BY q.Date`); err != nil {
		return fmt.Errorf("load QQQ/TQQQ bars: %w", err)
	}
	if len(days) < 250 {
		return fmt.Errorf("only %d aligned QQQ/TQQQ daily bars; run `market_history -symbols QQQ,TQQQ -start 2010-02-11` first", len(days))
	}
	for i := range days {
		d := &days[i]
		d.ShareRat = d.TVol / d.QVol
		d.DollarRat = (d.TVol * d.TClose) / (d.QVol * d.QClose)
		if i == 0 {
			d.DlogQ, d.DlogT = math.NaN(), math.NaN()
			continue
		}
		p := days[i-1]
		d.QRet, d.TRet = d.QClose/p.QClose-1, d.TClose/p.TClose-1
		d.DlogQ, d.DlogT = math.Log(d.QVol/p.QVol), math.Log(d.TVol/p.TVol)
	}

	rdb, err := sqlx.Open("sqlite", s.resultsDBPath)
	if err != nil {
		return fmt.Errorf("open results db: %w", err)
	}
	defer rdb.Close()

	w := &qtWriter{db: rdb}
	if err := w.init(); err != nil {
		return err
	}
	for _, d := range days {
		w.exec(`INSERT INTO qqq_tqqq_daily VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.Date, d.QClose, d.QVol, d.TClose, d.TVol, d.QClose*d.QVol, d.TClose*d.TVol, d.ShareRat, d.DollarRat,
			d.QRet, d.TRet, nanNil(d.DlogQ), nanNil(d.DlogT))
	}

	// Series aligned from row 1 (row 0 has no change).
	rows := days[1:]
	dq, dt := make([]float64, len(rows)), make([]float64, len(rows))
	lq, lt := make([]float64, len(days)), make([]float64, len(days))
	absQ := make([]float64, len(rows))
	for i, d := range rows {
		dq[i], dt[i], absQ[i] = d.DlogQ, d.DlogT, math.Abs(d.QRet)
	}
	for i, d := range days {
		lq[i], lt[i] = math.Log(d.QVol), math.Log(d.TVol)
	}

	// --- Overall summary ---
	shareRat, dollarRat := make([]float64, len(days)), make([]float64, len(days))
	for i, d := range days {
		shareRat[i], dollarRat[i] = d.ShareRat, d.DollarRat
	}
	slope, intercept, r2 := olsLine(lq, lt)
	sameDay := ComputeCorrelation(dq, dt)
	tVolAbsRet := ComputeCorrelation(dt, absQ)
	qVolAbsRet := ComputeCorrelation(dq, absQ)

	var bothUp, qUp, qUpUpDay, bothUpUpDay int
	for _, d := range rows {
		if d.DlogQ > 0 {
			qUp++
			if d.DlogT > 0 {
				bothUp++
			}
			if d.QRet > 0 {
				qUpUpDay++
				if d.DlogT > 0 {
					bothUpUpDay++
				}
			}
		}
	}
	summary := []struct {
		k string
		v float64
		n string
	}{
		{"days_aligned", float64(len(days)), fmt.Sprintf("%s to %s", days[0].Date, days[len(days)-1].Date)},
		{"corr_daily_volume_change", sameDay, "same-day correlation of ln(volume_t/volume_t-1), QQQ vs TQQQ"},
		{"corr_log_volume_levels", ComputeCorrelation(lq, lt), "correlation of ln(volume) levels (trends inflate this; prefer the change correlation)"},
		{"elasticity_tqqq_on_qqq", slope, "slope of ln(TQQQ vol) on ln(QQQ vol): a 1% rise in QQQ volume goes with this % rise in TQQQ volume"},
		{"elasticity_intercept", intercept, ""},
		{"elasticity_r2", r2, "share of TQQQ log-volume variance explained by QQQ log-volume"},
		{"median_share_volume_ratio", median(shareRat), "TQQQ shares traded per QQQ share traded"},
		{"median_dollar_volume_ratio", median(dollarRat), "TQQQ $ volume / QQQ $ volume"},
		{"corr_qqq_volchg_abs_qqq_ret", qVolAbsRet, "QQQ volume change vs size of QQQ's move"},
		{"corr_tqqq_volchg_abs_qqq_ret", tVolAbsRet, "TQQQ volume change vs size of QQQ's move (leverage should make this stronger)"},
		{"p_tqqq_volup_given_qqq_volup", ratio(bothUp, qUp), "share of QQQ volume-up days on which TQQQ volume was also up"},
		{"p_tqqq_volup_given_qqq_volup_and_up_day", ratio(bothUpUpDay, qUpUpDay), "same, restricted to days QQQ also closed up (the sig-qqq-up1 entry condition)"},
	}
	for _, r := range summary {
		w.exec(`INSERT INTO volume_summary VALUES (?,?,?)`, r.k, r.v, r.n)
	}

	// --- Lead / lag: corr(dQ[t], dT[t+k]); k>0 means QQQ volume change leads TQQQ. ---
	maxK := 5
	lagCorr := map[int]float64{}
	for k := -maxK; k <= maxK; k++ {
		var a, b []float64
		for i := range dq {
			j := i + k
			if j >= 0 && j < len(dt) {
				a, b = append(a, dq[i]), append(b, dt[j])
			}
		}
		lagCorr[k] = ComputeCorrelation(a, b)
		w.exec(`INSERT INTO volume_lag_corr VALUES (?,?)`, k, lagCorr[k])
	}

	// --- Granger, both directions, on volume changes ---
	granger := map[string][]GrangerResult{}
	for _, dir := range []struct {
		name, pn, tn string
		p, t         []float64
	}{
		{"QQQ->TQQQ", "QQQ", "TQQQ", dq, dt},
		{"TQQQ->QQQ", "TQQQ", "QQQ", dt, dq},
	} {
		res, err := ComputeGrangerCausality(dir.pn, dir.tn, dir.p, dir.t, maxK)
		if err != nil {
			log.Printf("Granger %s skipped: %v", dir.name, err)
			continue
		}
		granger[dir.name] = res
		for _, r := range res {
			w.exec(`INSERT INTO volume_granger VALUES (?,?,?)`, dir.name, r.LagMinutes, r.PValue)
		}
	}

	// --- Rolling 60-day correlation ---
	const win = 60
	var rollMin, rollMax = 2.0, -2.0
	for i := win; i <= len(dq); i++ {
		c := ComputeCorrelation(dq[i-win:i], dt[i-win:i])
		w.exec(`INSERT INTO volume_rolling_corr VALUES (?,?)`, rows[i-1].Date, c)
		rollMin, rollMax = math.Min(rollMin, c), math.Max(rollMax, c)
	}

	// --- By year ---
	byYear := map[string][]int{}
	var years []string
	for i, d := range rows {
		y := d.Date[:4]
		if _, ok := byYear[y]; !ok {
			years = append(years, y)
		}
		byYear[y] = append(byYear[y], i)
	}
	type yearRow struct {
		y                    string
		n                    int
		corr, sr, dr, qd, td float64
	}
	var yrows []yearRow
	for _, y := range years {
		idx := byYear[y]
		if len(idx) < 20 {
			continue
		}
		var a, b, sr, dr, qd, td []float64
		for _, i := range idx {
			d := rows[i]
			a, b = append(a, d.DlogQ), append(b, d.DlogT)
			sr, dr = append(sr, d.ShareRat), append(dr, d.DollarRat)
			qd, td = append(qd, d.QVol*d.QClose), append(td, d.TVol*d.TClose)
		}
		yr := yearRow{y, len(idx), ComputeCorrelation(a, b), median(sr), median(dr), mean(qd), mean(td)}
		yrows = append(yrows, yr)
		w.exec(`INSERT INTO volume_by_year VALUES (?,?,?,?,?,?,?)`, yr.y, yr.n, yr.corr, yr.sr, yr.dr, yr.qd, yr.td)
	}

	// --- By size/direction of QQQ's move ---
	type bucket struct {
		name   string
		lo, hi float64
	}
	buckets := []bucket{{"< -2%", -1, -0.02}, {"-2% to -1%", -0.02, -0.01}, {"-1% to 0", -0.01, 0}, {"0 to +1%", 0, 0.01}, {"+1% to +2%", 0.01, 0.02}, {"> +2%", 0.02, 1}}
	type moveRow struct {
		name       string
		n          int
		dr, qd, td float64
	}
	var mrows []moveRow
	for _, b := range buckets {
		var dr, qd, td []float64
		for _, d := range rows {
			if d.QRet >= b.lo && d.QRet < b.hi {
				dr, qd, td = append(dr, d.DollarRat), append(qd, d.DlogQ), append(td, d.DlogT)
			}
		}
		if len(dr) == 0 {
			continue
		}
		mr := moveRow{b.name, len(dr), median(dr), mean(qd), mean(td)}
		mrows = append(mrows, mr)
		w.exec(`INSERT INTO volume_by_qqq_move VALUES (?,?,?,?,?)`, mr.name, mr.n, mr.dr, mr.qd, mr.td)
	}
	if w.err != nil {
		return fmt.Errorf("write results: %w", w.err)
	}

	// --- Console report ---
	fmt.Printf("\n📊 %s\n   %d aligned trading days, %s → %s\n", s.Name(), len(days), days[0].Date, days[len(days)-1].Date)
	fmt.Printf("\nScale: TQQQ trades a median %.2f shares and $%.2f per $1 of QQQ volume.\n", median(shareRat), median(dollarRat))
	fmt.Printf("\nDo daily volume changes move together?\n   same-day corr %.2f   |   log-level corr %.2f (trend-inflated)   |   elasticity %.2f (R² %.2f)\n",
		sameDay, ComputeCorrelation(lq, lt), slope, r2)
	fmt.Printf("   when QQQ volume is up, TQQQ volume is also up %.0f%% of the time (%.0f%% if QQQ also closed up)\n",
		100*ratio(bothUp, qUp), 100*ratio(bothUpUpDay, qUpUpDay))
	fmt.Printf("\nLead/lag corr of volume changes (k>0: QQQ leads TQQQ by k days):\n  ")
	for k := -maxK; k <= maxK; k++ {
		fmt.Printf(" %+d:%.2f", k, lagCorr[k])
	}
	fmt.Println()
	for _, name := range []string{"QQQ->TQQQ", "TQQQ->QQQ"} {
		if res, ok := granger[name]; ok {
			fmt.Printf("Granger %s p-values by lag:", name)
			for _, r := range res {
				fmt.Printf(" %d:%.3f", r.LagMinutes, r.PValue)
			}
			fmt.Println()
		}
	}
	fmt.Printf("Rolling 60d correlation ranged %.2f to %.2f.\n", rollMin, rollMax)
	fmt.Printf("\n%-6s %5s %9s %11s %12s %14s %14s\n", "Year", "Days", "Corr", "Med share×", "Med dollar×", "QQQ avg $vol", "TQQQ avg $vol")
	for _, r := range yrows {
		fmt.Printf("%-6s %5d %9.2f %11.2f %12.2f %14.2e %14.2e\n", r.y, r.n, r.corr, r.sr, r.dr, r.qd, r.td)
	}
	fmt.Printf("\n%-13s %5s %13s %15s %16s\n", "QQQ move", "Days", "Med $ ratio", "Avg ΔlnVol QQQ", "Avg ΔlnVol TQQQ")
	for _, r := range mrows {
		fmt.Printf("%-13s %5d %13.3f %15.3f %16.3f\n", r.name, r.n, r.dr, r.qd, r.td)
	}
	return nil
}

// qtWriter batches result-table writes and remembers the first error.
type qtWriter struct {
	db  *sqlx.DB
	err error
}

func (w *qtWriter) exec(q string, args ...any) {
	if w.err == nil {
		_, w.err = w.db.Exec(q, args...)
	}
}

func (w *qtWriter) init() error {
	_, err := w.db.Exec(`
		DROP TABLE IF EXISTS qqq_tqqq_daily;
		DROP TABLE IF EXISTS volume_summary;
		DROP TABLE IF EXISTS volume_lag_corr;
		DROP TABLE IF EXISTS volume_granger;
		DROP TABLE IF EXISTS volume_rolling_corr;
		DROP TABLE IF EXISTS volume_by_year;
		DROP TABLE IF EXISTS volume_by_qqq_move;
		CREATE TABLE qqq_tqqq_daily (date TEXT PRIMARY KEY, qqq_close REAL, qqq_volume REAL, tqqq_close REAL, tqqq_volume REAL,
			qqq_dollar_volume REAL, tqqq_dollar_volume REAL, share_volume_ratio REAL, dollar_volume_ratio REAL,
			qqq_ret REAL, tqqq_ret REAL, dlog_qqq_volume REAL, dlog_tqqq_volume REAL);
		CREATE TABLE volume_summary (metric TEXT PRIMARY KEY, value REAL, note TEXT);
		CREATE TABLE volume_lag_corr (lag_days INTEGER PRIMARY KEY, corr REAL);
		CREATE TABLE volume_granger (direction TEXT, lag_days INTEGER, p_value REAL);
		CREATE TABLE volume_rolling_corr (date TEXT PRIMARY KEY, corr_60d REAL);
		CREATE TABLE volume_by_year (year TEXT PRIMARY KEY, days INTEGER, corr_dlog_volume REAL, median_share_ratio REAL,
			median_dollar_ratio REAL, qqq_avg_dollar_volume REAL, tqqq_avg_dollar_volume REAL);
		CREATE TABLE volume_by_qqq_move (qqq_move TEXT PRIMARY KEY, days INTEGER, median_dollar_ratio REAL,
			avg_dlog_qqq_volume REAL, avg_dlog_tqqq_volume REAL);`)
	return err
}

func nanNil(v float64) any {
	if math.IsNaN(v) {
		return nil
	}
	return v
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	if n := len(c); n%2 == 0 {
		return (c[n/2-1] + c[n/2]) / 2
	}
	return c[len(c)/2]
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// olsLine fits y = intercept + slope*x and returns slope, intercept and R².
func olsLine(x, y []float64) (slope, intercept, r2 float64) {
	n := float64(len(x))
	if n < 2 {
		return 0, 0, 0
	}
	mx, my := mean(x), mean(y)
	var sxy, sxx, syy float64
	for i := range x {
		sxy += (x[i] - mx) * (y[i] - my)
		sxx += (x[i] - mx) * (x[i] - mx)
		syy += (y[i] - my) * (y[i] - my)
	}
	if sxx == 0 || syy == 0 {
		return 0, my, 0
	}
	slope = sxy / sxx
	return slope, my - slope*mx, sxy * sxy / (sxx * syy)
}
