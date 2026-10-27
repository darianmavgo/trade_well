// Package ready answers "will tonight's scan and tomorrow's orders work?" before they run:
// the allowlist resolves from the bundle compiled into this binary, the Markov models and the
// market data are current, the Schwab token has days left, and the accounts have funds. It
// looks forward only: what already went wrong (a missed or failed job slot) is check_health's
// report, not a reason to call the next run unready. It backs the /ready page, `orchestrator ready`, the
// /cron/ready email, deploy.sh's pre- and post-deploy checks and the weekly digest.
package ready

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	sqlfiles "github.com/darianmavgo/trade_well/sql"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/modelstate"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
	"github.com/darianmavgo/trade_well/pkg/schwaber/tokensync"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// JobName is the cron job, CLI subcommand, page route and job_run name.
const JobName = "ready"

// Check statuses, worst last.
const (
	OK   = "ok"
	Warn = "warn"
	Bad  = "bad"
)

// RefreshTokenLife is how long a Schwab refresh token stays valid after it is issued.
const RefreshTokenLife = 7 * 24 * time.Hour

// Check is one thing that has to be true for the pipeline to work.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Report is every check and the worst status among them.
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	Status      string    `json:"status"`
	Checks      []Check   `json:"checks"`
}

// Options are the inputs of Run. Nothing is read from the process environment here.
type Options struct {
	DB        *sql.DB // orchestrator database; nil skips the checks that need it
	Now       time.Time
	Allowlist []string
	MarketDB  string
	TokenPath string
	// Static runs only the allowlist-and-bundle check, which needs no database, market data
	// or broker. deploy.sh uses it before it uploads anything.
	Static bool
}

func worse(a, b string) string {
	rank := map[string]int{OK: 0, Warn: 1, Bad: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Run performs the checks. It never returns an error: a check that cannot run is itself a
// warn or bad result, so the page and the email always say something.
func Run(ctx context.Context, o Options) Report {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	rep := Report{GeneratedAt: now.UTC(), Status: OK}
	add := func(c Check) {
		rep.Checks = append(rep.Checks, c)
		rep.Status = worse(rep.Status, c.Status)
	}

	members, c := allowlistCheck(o.Allowlist)
	add(c)
	add(allocationCheck(members))
	add(sqlFilesCheck())
	if o.Static {
		return rep
	}
	completed := calendar.LastCompletedSession(now)
	add(modelsCheck(members, o.MarketDB, completed))
	add(marketCheck(members, o.DB, o.MarketDB, completed))
	add(hmmCheck(ctx, o.DB, completed))
	add(tokenCheck(ctx, o.TokenPath, now))
	if o.DB != nil {
		add(killswitchCheck(o.DB))
		add(cashCheck(o.DB))
	}
	add(databaseCheck(ctx, o.DB))
	return rep
}

// sqlFilesCheck reports where this process found its sql folder and that the SQL the jobs read is
// in it. The SQL is read from files, not embedded in the binary, so a deploy that did not upload
// sql/ (or an instance that cannot see it) fails here, in the detail, before a job fails on it.
func sqlFilesCheck() Check {
	c := Check{Name: "sql_files", Status: OK}
	root, err := sqlfiles.Root()
	if err != nil {
		c.Status, c.Detail = Bad, err.Error()
		return c
	}
	var files int
	err = fs.WalkDir(sqlfiles.Sub("orchestrator"), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".sql") {
			files++
		}
		return nil
	})
	if err != nil || files == 0 {
		c.Status, c.Detail = Bad, fmt.Sprintf("the sql folder %s has no orchestrator SQL files (%v): the deploy did not upload sql/orchestrator", root, err)
		return c
	}
	if _, err := fs.Stat(sqlfiles.FS, "stages"); err != nil {
		c.Status = Warn
		c.Detail = fmt.Sprintf("sql folder %s: %d orchestrator SQL files, but no stages/ folder for the backtest layer's SQL", root, files)
		return c
	}
	c.Detail = fmt.Sprintf("sql folder %s: %d orchestrator SQL files, and the backtest layer's SQL", root, files)
	return c
}

// allowlistCheck resolves every member of every allowlist entry. A row-backed strategy must be
// in the bundle compiled into this binary: finding it in a full strategies.db on this machine
// proves nothing about App Engine, which only has the bundle.
func allowlistCheck(allowlist []string) ([]strategy.Strategy, Check) {
	c := Check{Name: "allowlist"}
	if len(allowlist) == 0 {
		c.Status, c.Detail = Bad, "STRATEGY_ALLOWLIST is empty: every job refuses to run"
		return nil, c
	}
	bundle, err := strategies.ReadBundledStrategyAssetInfo()
	if err != nil {
		c.Status, c.Detail = Bad, "cannot read the bundle: "+err.Error()
		return nil, c
	}
	var problems []string
	var members []strategy.Strategy
	seen := map[string]bool{}
	for _, entry := range allowlist {
		ids := []string{entry}
		if strategy.IsStack(entry) {
			ids = strategy.ParseStack(entry)
		}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			got, err := strategies.ResolveGreenlit(nil, []string{id})
			if err != nil || len(got) == 0 {
				problems = append(problems, fmt.Sprintf("%s: not registered", id))
				continue
			}
			members = append(members, got[0])
			if _, isRow := got[0].(interface{ Family() string }); isRow && !slices.Contains(bundle.IDs, id) {
				problems = append(problems, fmt.Sprintf("%s: a database row that is not in the bundle (resolves on this machine only; run scripts/export_strategies.sh)", id))
			}
		}
	}
	if len(problems) > 0 {
		c.Status, c.Detail = Bad, strings.Join(problems, "; ")
		return members, c
	}
	c.Status = OK
	c.Detail = fmt.Sprintf("%d strateg(ies) in %d entr(ies) resolve; bundle %s holds %d rows", len(members), len(allowlist), bundle.Hash, len(bundle.IDs))
	return members, c
}

// allocationCheck compares the allocation the bundle was exported at (the backtest's flat share
// of equity per position) with what each percent-sized member would really trade. A member
// sized at a different share is trading something the backtest never ran.
func allocationCheck(members []strategy.Strategy) Check {
	c := Check{Name: "allocation"}
	bundle, err := strategies.ReadBundledStrategyAssetInfo()
	if err != nil {
		c.Status, c.Detail = Bad, "cannot read the bundle: "+err.Error()
		return c
	}
	if bundle.Alloc <= 0 {
		c.Status, c.Detail = Warn, "the bundle records no allocation: export with `strategy export -alloc <the backtest's -alloc>` (scripts/export_strategies.sh does)"
		return c
	}
	cfgs := make(map[string]strategy.StrategyConfig, len(members))
	for _, m := range members {
		cfgs[m.ID()] = m.DefaultConfig()
	}
	off, n := allocationOff(bundle.Alloc, cfgs)
	if len(off) > 0 {
		c.Status, c.Detail = Bad, fmt.Sprintf("the bundle is exported at %.1f%% per position but %s", bundle.Alloc*100, strings.Join(off, "; "))
		return c
	}
	c.Status = OK
	c.Detail = fmt.Sprintf("%d percent-sized strateg(ies) trade %.1f%% of equity per position, as exported", n, bundle.Alloc*100)
	return c
}

// allocationOff lists the percent-sized strategies in cfgs whose allocation is not want, and how
// many percent-sized ones there are. A strategy sized by its own rule (fixed shares or dollars)
// is not a share of equity and is left out of both.
func allocationOff(want float64, cfgs map[string]strategy.StrategyConfig) (off []string, n int) {
	ids := make([]string, 0, len(cfgs))
	for id := range cfgs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cfg := cfgs[id]
		if s := strings.ToLower(strings.TrimSpace(cfg.PositionSizing)); s == "fixed_shares" || s == "fixed_dollar" {
			continue
		}
		n++
		if math.Abs(cfg.AllocationPct-want) > 1e-9 {
			off = append(off, fmt.Sprintf("%s sizes %.1f%%", id, cfg.AllocationPct*100))
		}
	}
	return off, n
}

// gap grades how many sessions a date trails the last completed session. The evening scan is
// what brings the data current, so one session behind is normal before it.
func gap(date string, completed time.Time) (int, string) {
	if len(date) > 10 {
		date = date[:10]
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 99, Bad
	}
	n := calendar.TradingDaysBetween(t, completed)
	switch {
	case n <= 1:
		return n, OK
	case n == 2:
		return n, Warn
	}
	return n, Bad
}

// modelsCheck compares each Markov model with the market data it is scored from. A model only
// moves when the evening scan runs Prepare, and an App Engine instance starts every day from
// the bundled models (exported on an earlier date), so "the model trails the last session" is
// the normal state until that scan. What matters is whether the scan can catch it up: the market
// data must already run past the model's last date. Then it is a warning that says so. It is bad
// when a model is missing, or when the market data is no newer than the model and the pair is
// still behind (the scan has nothing to score from).
func modelsCheck(members []strategy.Strategy, marketDB string, completed time.Time) Check {
	c := Check{Name: "markov_models", Status: OK}
	path := appenv.MarkovDB()
	var lines []string
	for _, m := range members {
		f, ok := m.(interface{ Family() string })
		if !ok || f.Family() != "markov" {
			continue
		}
		sym := strings.ToUpper(strings.TrimSpace(m.DefaultConfig().Benchmark))
		last, ok := readMarkovModelAssetLastDate(path, sym)
		if !ok {
			c.Status = worse(c.Status, Bad)
			lines = append(lines, fmt.Sprintf("%s: no saved model (the scan will train one from the instance's bars)", sym))
			continue
		}
		n, st := gap(last, completed)
		learned := readMarkovModelAssetTrainedThrough(path, sym)
		if st != OK {
			market, merr := readMarketWorkingCopyLastDate(marketDB, sym)
			if (merr != nil || market == "") && catchableByDownload(last, completed) {
				// No market file yet (a cold instance, or Turso with nothing stored): the scan
				// downloads the window first, and that window reaches back past the model.
				st = Warn
				lines = append(lines, fmt.Sprintf("%s: scored through %s (%d session(s) behind), no market data on this instance yet, so the next scan downloads it and catches up; learned through %s", sym, last, n, learned))
				c.Status = worse(c.Status, st)
				continue
			}
			if merr == nil && market > last {
				// The data is there; the next scan's Prepare scores the sessions in between.
				st = Warn
				lines = append(lines, fmt.Sprintf("%s: scored through %s (%d session(s) behind), market data runs to %s, so the next scan catches it up; learned through %s", sym, last, n, market, learned))
				c.Status = worse(c.Status, st)
				continue
			}
		}
		c.Status = worse(c.Status, st)
		lines = append(lines, fmt.Sprintf("%s: through %s (%d session(s) behind), learned through %s", sym, last, n, learned))
	}
	if len(lines) == 0 {
		c.Detail = "no Markov strategy in the allowlist"
		return c
	}
	c.Detail = strings.Join(lines, "; ")
	return c
}

// maxCatchUpDays is how far back from the last completed session a live scan's download reaches
// with room to spare: it fetches minBars/240+1 years of daily bars (a year for the strategies in
// use), and a model is scored from a 20-session return, so its last date must sit well inside
// that window. A model further behind needs a deliberate retrain, not a scan.
const maxCatchUpDays = 330

func catchableByDownload(lastDate string, completed time.Time) bool {
	if len(lastDate) > 10 {
		lastDate = lastDate[:10]
	}
	t, err := time.Parse("2006-01-02", lastDate)
	return err == nil && completed.Sub(t) <= maxCatchUpDays*24*time.Hour
}

// marketCheck reports how current the daily bars are for every symbol the allowlist trades. It
// reads the local market file when the instance has one. A cold instance has none (the scan
// rebuilds it from Turso), so it reads the Turso market_bars the scan keeps instead; with
// neither it can only warn.
func marketCheck(members []strategy.Strategy, db *sql.DB, marketDB string, completed time.Time) Check {
	c := Check{Name: "market_data", Status: OK}
	if marketDB == "" {
		marketDB = appenv.MarketDB()
	}
	tipOf := func(sym string) (string, error) { return readMarketWorkingCopyLastDate(marketDB, sym) }
	source := "local market file"
	if fi, err := os.Stat(marketDB); err != nil || fi.Size() == 0 {
		if db == nil {
			c.Status, c.Detail = Warn, "no market database at "+marketDB+" yet and no orchestrator database to read the bars from: the scan creates the file from the market data source"
			return c
		}
		source = "Turso market_bars"
		tipOf = func(sym string) (string, error) {
			var tip sql.NullString
			err := db.QueryRow(orchestratorsql.Q("ready/market_check"), sym).Scan(&tip)
			return tip.String, err
		}
	}
	seen := map[string]bool{}
	var syms []string
	for _, m := range members {
		if rp, ok := m.(strategy.RequiredSymbolsProvider); ok {
			for _, s := range rp.RequiredSymbols() {
				s = strings.ToUpper(strings.TrimSpace(s))
				if s != "" && !seen[s] {
					seen[s] = true
					syms = append(syms, s)
				}
			}
		}
	}
	sort.Strings(syms)
	var lines []string
	for _, s := range syms {
		tip, err := tipOf(s)
		if err != nil || tip == "" {
			c.Status = worse(c.Status, Bad)
			lines = append(lines, s+": no daily bars")
			continue
		}
		n, st := gap(tip, completed)
		c.Status = worse(c.Status, st)
		lines = append(lines, fmt.Sprintf("%s: %s (%d behind)", s, tip, n))
	}
	c.Detail = source + ", last daily bar, last completed session " + completed.Format("2006-01-02") + ": " + strings.Join(lines, "; ")
	return c
}

func tokenCheck(ctx context.Context, path string, now time.Time) Check {
	c := Check{Name: "schwab_token"}
	if path == "" {
		c.Status, c.Detail = Warn, "no token path configured"
		return c
	}
	_ = tokensync.SyncBeforeUse(ctx, path)
	data, err := os.ReadFile(path)
	if err != nil {
		c.Status, c.Detail = Bad, "no token file: "+err.Error()
		return c
	}
	var tok struct {
		Issued time.Time `json:"refresh_token_issued"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || tok.Issued.IsZero() {
		c.Status, c.Detail = Bad, "token file has no refresh_token_issued"
		return c
	}
	left := tok.Issued.Add(RefreshTokenLife).Sub(now)
	switch {
	case left <= 0:
		c.Status = Bad
		c.Detail = "refresh token expired " + (-left).Round(time.Hour).String() + " ago: run `orchestrator auth`"
	case left < 48*time.Hour:
		c.Status = Warn
		c.Detail = fmt.Sprintf("refresh token expires in %s: re-run `orchestrator auth` before then", left.Round(time.Hour))
	default:
		c.Status = OK
		c.Detail = fmt.Sprintf("refresh token valid for %.1f more days", left.Hours()/24)
	}
	return c
}

func killswitchCheck(db *sql.DB) Check {
	c := Check{Name: "killswitch", Status: OK, Detail: "released"}
	st, err := killswitch.Current(db)
	if err != nil {
		return Check{Name: "killswitch", Status: Warn, Detail: "cannot read it: " + err.Error()}
	}
	if st.Engaged {
		c.Status, c.Detail = Bad, st.String()+": every /cron route is refused"
	}
	return c
}

// cashCheck reports what each account can trade with, using the same rule the jobs size orders
// by (trader.GetBuyingPower). It does not read the available_funds column: Schwab leaves that, and
// buyingPower, at 0 on a CASH account (a Roth) and reports cashAvailableForTrading instead.
func cashCheck(db *sql.DB) Check {
	c := Check{Name: "cash", Status: OK}
	rows, err := db.Query(orchestratorsql.Q("ready/cash_check"))
	if err != nil {
		return Check{Name: "cash", Status: Warn, Detail: "no account snapshot yet: " + err.Error()}
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var acct, kind, raw, fetched string
		var funds float64
		if err := rows.Scan(&acct, &kind, &funds, &raw, &fetched); err != nil {
			return Check{Name: "cash", Status: Warn, Detail: err.Error()}
		}
		var sec trader.SecuritiesAccount
		if raw != "" && json.Unmarshal([]byte(raw), &sec) == nil {
			funds = trader.GetBuyingPower(&sec)
		}
		if funds <= 0 {
			c.Status = worse(c.Status, Warn)
		}
		lines = append(lines, fmt.Sprintf("%s %s: $%.2f to trade with (as of %s)", masked(acct), strings.ToLower(kind), funds, short(fetched)))
	}
	if len(lines) == 0 {
		return Check{Name: "cash", Status: Warn, Detail: "no account in the snapshot: account_state has not run"}
	}
	c.Detail = strings.Join(lines, "; ")
	return c
}

func masked(acct string) string {
	if len(acct) > 4 {
		return "…" + acct[len(acct)-4:]
	}
	return acct
}

func short(t string) string {
	if len(t) > 16 {
		return t[:16]
	}
	return t
}

// tursoTables are the tables the scheduled jobs read and write. Each is created by the job
// that owns it, so one missing from a database that has run the pipeline means the data did
// not come across, not that a job has yet to run.
var tursoTables = []string{
	"job_run", "account_state_account", "account_state_order", "account_state_transaction",
	"stage_order", "submit_order", "order_intents", "manage_exit", "risk_state", "risk_equity",
	"check_health", "scan_market_signal", "markov_prediction_live", "market_bars", "send_email",
}

// databaseCheck says whether the orchestrator database is one this deployment can run on:
// Turso, because an instance's disk is gone when it stops, and the GCS bucket holds
// only the config object and secrets, so a deployment without Turso loses its data. The check
// reads the live database, so it is the proof that every job is reading and writing Turso.
func databaseCheck(ctx context.Context, db *sql.DB) Check {
	c := Check{Name: "database"}
	_, remote, derr := dbutil.Turso()
	switch {
	case derr != nil:
		c.Status, c.Detail = Bad, "Turso is configured wrongly: "+derr.Error()
		return c
	case !remote:
		c.Status, c.Detail = Bad, "TURSO_DATABASE_URL is not set: Turso is the only supported database (a local file is lost when an instance stops and drifts from the deployment). Run deploy_gae/setup_turso.sh"
		return c
	}
	if db == nil {
		c.Status, c.Detail = Warn, "Turso is configured but no database handle was given, so it was not read"
		return c
	}
	if err := db.PingContext(ctx); err != nil {
		c.Status, c.Detail = Bad, "Turso does not answer: "+err.Error()
		return c
	}
	var missing []string
	for _, t := range tursoTables {
		var n int
		if err := db.QueryRowContext(ctx, orchestratorsql.Q("ready/database_check"), t).Scan(&n); err != nil {
			c.Status, c.Detail = Bad, "cannot read Turso: "+err.Error()
			return c
		}
		if n == 0 {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		c.Status, c.Detail = Bad, "Turso is reachable but these tables are missing: "+strings.Join(missing, ", ")
		return c
	}
	var jobs int
	var newest sql.NullString
	if err := db.QueryRowContext(ctx, orchestratorsql.Q("ready/database_check_2")).Scan(&jobs, &newest); err != nil {
		c.Status, c.Detail = Bad, "cannot read job_run on Turso: "+err.Error()
		return c
	}
	c.Status = OK
	c.Detail = fmt.Sprintf("jobs read and write Turso: all %d tables present, %d job runs recorded, newest %s", len(tursoTables), jobs, short(newest.String))
	return c
}

// hmmRetrainAfter is how long after its training cutoff the QQQ hidden Markov model is flagged
// for a deliberate retrain: its parameters are frozen, so they age.
const hmmRetrainAfter = 730 * 24 * time.Hour

// hmmCheck answers whether the hmm strategies know QQQ's regime for the next session. The
// evening scan's Prepare scores each new session and persists it to Turso (hmm_prediction_live);
// the bundled model only holds what was known when it was exported, and an App Engine instance
// starts from it. So the newest of the two is graded, not the bundle alone. Its row for the last
// completed session carries the next-session regime the strategies trade on: OK only when that
// session is scored, a warning one session behind (before the scan runs), bad further behind.
// No bundled model is bad: every hmm strategy fails to scan.
func hmmCheck(ctx context.Context, db *sql.DB, completed time.Time) Check {
	c := Check{Name: "hmm_model", Status: OK}
	if err := strategies.EnsureBundle(); err != nil {
		c.Status, c.Detail = Bad, "cannot place the bundled models: "+err.Error()
		return c
	}
	l, err := readHMMModelAssetLatest(appenv.HMMDB(), "QQQ")
	if err != nil {
		c.Status, c.Detail = Bad, "no saved QQQ hidden Markov model in the bundle, so every hmm strategy fails to scan (train hmm in backtestgosqlite, then scripts/export_strategies.sh): "+err.Error()
		return c
	}
	date, regime, regimeProb, next, nextProb, source := l.Date, l.Regime, l.RegimeProb, l.NextRegime, l.NextRegimeProb, "the bundle"
	var notes []string
	if db != nil {
		trainedAt, err := modelstate.ReadHMMModelAssetTrainedAt(appenv.HMMDB(), "QQQ")
		if err == nil {
			var tip modelstate.HMMTip
			tip, err = modelstate.LatestHMMPredictionInTurso(ctx, db, "QQQ", trainedAt)
			if err == nil && tip.Date > date {
				date, regime, regimeProb, next, nextProb, source = tip.Date, tip.Regime, tip.RegimeProb, tip.NextRegime, tip.NextRegimeProb, "Turso (persisted by Prepare)"
			}
		}
		if err != nil && err != sql.ErrNoRows {
			notes = append(notes, "could not read Turso predictions: "+err.Error())
		}
		if run, err := job_runs.LastJobRun(db, "scan_market"); err == nil && run != nil {
			notes = append(notes, fmt.Sprintf("last scan_market run %s at %s", run.Status, run.FinishedAt.UTC().Format("2006-01-02 15:04Z")))
		} else if err == nil {
			notes = append(notes, "scan_market has never run")
		}
	} else {
		notes = append(notes, "Turso not consulted")
	}
	n, _ := gap(date, completed)
	c.Detail = fmt.Sprintf("QQQ regime %d (%.0f%%) as of %s from %s; regime for the next session %d (%.0f%%); last completed session %s",
		regime, regimeProb*100, date, source, next, nextProb*100, completed.Format("2006-01-02"))
	switch {
	case n == 1:
		c.Status = Warn
		c.Detail += "; Prepare has not scored the last session yet, so the next-session regime is one session stale until the evening scan runs"
	case n > 1:
		c.Status = Bad
		c.Detail += fmt.Sprintf("; %d sessions behind: Prepare is not scoring, the next-session regime is stale", n)
	}
	if len(notes) > 0 {
		c.Detail += "; " + strings.Join(notes, "; ")
	}
	if t, err := time.Parse("2006-01-02", l.TrainedThrough); err == nil && completed.Sub(t) > hmmRetrainAfter {
		c.Status = worse(c.Status, Warn)
		c.Detail += "; the parameters are over two years old, consider a deliberate retrain (train hmm)"
	}
	return c
}
