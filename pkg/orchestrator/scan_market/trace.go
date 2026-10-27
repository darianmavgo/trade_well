package scan_market

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
)

// TraceRow is one strategy's explanation for one session: whether it entered, the reason in a
// sentence, and the numbers behind it.
type TraceRow struct {
	ScanRunID string `json:"scan_run_id"`
	CreatedAt string `json:"created_at"`
	Strategy  string `json:"strategy"`
	Symbol    string `json:"symbol"`
	Date      string `json:"date"`
	AsOf      bool   `json:"as_of"`
	Entry     bool   `json:"entry"`
	Reason    string `json:"reason"`
	FactsText string `json:"facts_text"`
	FactsJSON string `json:"-"`
}

// SaveTrace stores the trace the pipelines left for each strategy of a scan. A row is the nightly
// verdict (is_asof) when its session is the scan's as-of bar.
func SaveTrace(db *sql.DB, scanRunID, asOf string, rows []runner.SignalScanRow, now time.Time) (int, error) {
	if err := EnsureSchema(db); err != nil {
		return 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, r := range rows {
		for _, t := range r.Trace {
			asof := 0
			if t.Date == asOf {
				asof = 1
			}
			entry := 0
			if t.Entry {
				entry = 1
			}
			if _, err := tx.Exec(orchestratorsql.Q("scan_market/save_trace"),
				scanRunID, r.StrategyID, t.Symbol, t.Date, asof, entry, t.Reason, string(t.Facts), now.UTC().Format(time.RFC3339)); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, tx.Commit()
}

// SavePredictions copies the Markov predictions scored after each model's training cutoff from the
// model database into markov_prediction_live, for the symbols the trace names. It returns the rows
// written. A missing model database writes nothing: the scan must not fail for an audit copy.
func SavePredictions(db *sql.DB, scanRunID string, rows []runner.SignalScanRow, now time.Time) (int, error) {
	seen := map[string]bool{}
	var symbols []string
	for _, r := range rows {
		for _, t := range r.Trace {
			var f struct {
				Model string `json:"model_symbol"`
			}
			if json.Unmarshal(t.Facts, &f) == nil && f.Model != "" && !seen[f.Model] {
				seen[f.Model] = true
				symbols = append(symbols, f.Model)
			}
		}
	}
	return CopyMarkovModelAssetPredictionsToTurso(db, appenv.MarkovDB(), scanRunID, symbols, now)
}

// RecordTrace stores a scan's trace and live predictions. It logs and carries on when either
// fails: the trace explains a decision and must never stop a scan.
func RecordTrace(db *sql.DB, scanRunID, asOf string, rows []runner.SignalScanRow, now time.Time) (traces, predictions int) {
	traces, err := SaveTrace(db, scanRunID, asOf, rows, now)
	if err != nil {
		log.Printf("[scan_market] save trace: %v", err)
	}
	predictions, err = SavePredictions(db, scanRunID, rows, now)
	if err != nil {
		log.Printf("[scan_market] save live predictions: %v", err)
	}
	return traces, predictions
}

// factsText turns a trace's facts JSON into "key=value" pairs, the interesting keys first.
func factsText(raw string) string {
	if raw == "" {
		return ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return raw
	}
	skip := map[string]bool{"model_symbol": true, "rule": true, "signal_symbol": true, "regime": true, "streak_column": true}
	keys := make([]string, 0, len(m))
	for k := range m {
		if !skip[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := m[k]
		switch x := v.(type) {
		case float64:
			parts = append(parts, fmt.Sprintf("%s=%s", k, trimFloat(x)))
		case nil:
			parts = append(parts, k+"=none")
		default:
			parts = append(parts, fmt.Sprintf("%s=%v", k, x))
		}
	}
	return strings.Join(parts, " ")
}

func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", f), "0"), ".")
}

// RecentTrace reads the nightly verdicts (the as-of session of each scan) of the last days days,
// newest first, for the strategy ids in ids (nil keeps every strategy).
func RecentTrace(db *sql.DB, now time.Time, days int, ids []string) ([]TraceRow, error) {
	return readTrace(db, `WHERE is_asof = 1 AND session_date >= ?`, []any{now.UTC().AddDate(0, 0, -days).Format("2006-01-02")}, ids)
}

// LatestTrace reads, for each strategy, the sessions the newest scan explained, newest session first.
func LatestTrace(db *sql.DB, ids []string) ([]TraceRow, error) {
	return readTrace(db, `WHERE scan_run_id = (SELECT MAX(scan_run_id) FROM scan_market_trace)`, nil, ids)
}

func readTrace(db *sql.DB, where string, args []any, ids []string) ([]TraceRow, error) {
	out := []TraceRow{}
	if db == nil {
		return out, fmt.Errorf("scan_market trace: nil db")
	}
	if err := EnsureSchema(db); err != nil {
		return out, err
	}
	rows, err := db.Query(`SELECT scan_run_id, created_at, strategy_id, symbol, session_date, is_asof, entry, reason, facts_json
		FROM scan_market_trace `+where+` ORDER BY scan_run_id DESC, strategy_id, session_date DESC`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var t TraceRow
		var asof, entry int
		if err := rows.Scan(&t.ScanRunID, &t.CreatedAt, &t.Strategy, &t.Symbol, &t.Date, &asof, &entry, &t.Reason, &t.FactsJSON); err != nil {
			return out, err
		}
		t.AsOf, t.Entry = asof == 1, entry == 1
		t.FactsText = factsText(t.FactsJSON)
		if keep(ids, t.Strategy) {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}
