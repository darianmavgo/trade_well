package live_vs_backtest_trades

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/hoursim"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
)

type buySig struct {
	id         int64
	strategyID string
	symbol     string
	asOf       string
	next       string
	close      float64
	buyLimit   float64
	takeProfit float64
	stopLoss   float64
	holdDays   int
	status     string
	notes      string
	stagedID   int64
}

type stageRow struct {
	id       int64
	signalID int64
	account  string
	strategy string
	symbol   string
	qty      float64
	entry    float64
	tp       float64
	sl       float64
	hold     int
	status   string
	reason   string
	orderID  string
	stagedAt string
}

type exitRow struct {
	id          int64
	account     string
	symbol      string
	strategy    string
	strategyID  string
	asOf        string
	next        string
	signalID    int64
	buySignalID int64
	stageID     int64
	orderID     string
	entryLimit  float64
	entryFill   float64
	exitFill    float64
	exitPrice   float64
	filledQty   float64
	fees        float64
	qty         float64
	status      string
	exitReason  string
	reasonCode  string
	exitDate    string
	pnlPct      float64
	entryTime   string
	filledAt    string
	hold        int
}

// ledger is one live_vs_backtest_trade row before it is written.
type ledger struct {
	account, strategy, symbol, asOf, next      string
	buySignalID, signalID, stageID, exitID     int64
	orderID, liveOutcome, skipReason           string
	entryLimit, entryFill, exitFill, qty, fees float64
	liveExitDate, liveReason                   string
	liveReturn                                 float64
	modelOutcome                               string
	modelEntry, modelExit                      float64
	modelExitDate, modelReason                 string
	modelReturn                                float64
	entryBps, exitBps, limitBps                sql.NullFloat64
	delay                                      sql.NullFloat64
	signalMatch, outcomeMatch                  int
	close, tp, sl                              float64
	hold                                       int
}

func assemble(db *sql.DB, account, marketDB string, bars map[string][]models.Bar) ([]ledger, error) {
	if err := backfillFills(db); err != nil {
		return nil, err
	}
	sigs, err := loadSignals(db)
	if err != nil {
		return nil, err
	}
	stages, err := loadStages(db)
	if err != nil {
		return nil, err
	}
	exits, err := loadExits(db)
	if err != nil {
		return nil, err
	}
	rows := liveRows(sigs, stages, exits, account)
	applyModel(rows, marketDB, bars)
	return rows, nil
}

func liveRows(sigs []buySig, stages []stageRow, exits []exitRow, account string) []ledger {
	dedup := latestSignals(sigs)
	byID := map[int64]buySig{}
	for _, s := range dedup {
		byID[s.id] = s
	}
	exitByStage := map[int64]exitRow{}
	exitByOrder := map[string]exitRow{}
	for _, e := range exits {
		if e.stageID > 0 {
			exitByStage[e.stageID] = e
		}
		if e.orderID != "" {
			exitByOrder[e.orderID] = e
		}
	}
	usedSig := map[int64]bool{}
	usedExit := map[int64]bool{}
	var rows []ledger
	for _, so := range stages {
		if account != "" && so.account != account {
			continue
		}
		sig, ok := matchSignal(dedup, so)
		var ex *exitRow
		if e, hit := exitByStage[so.id]; hit {
			ex = &e
		} else if e, hit := exitByOrder[so.orderID]; hit && so.orderID != "" {
			ex = &e
		}
		row := fromStage(so, sig, ok, ex)
		rows = append(rows, row)
		if ok {
			usedSig[sig.id] = true
		}
		if ex != nil {
			usedExit[ex.id] = true
		}
	}
	for _, sig := range dedup {
		if usedSig[sig.id] {
			continue
		}
		if sig.stagedID > 0 {
			continue // staged for an account this run did not include
		}
		rows = append(rows, fromSignal(sig))
	}
	for _, ex := range exits {
		if usedExit[ex.id] {
			continue
		}
		if account != "" && ex.account != account {
			continue
		}
		rows = append(rows, fromExit(ex))
	}
	return rows
}

func latestSignals(in []buySig) []buySig {
	type key struct{ strategy, symbol, asOf string }
	best := map[key]buySig{}
	for _, s := range in {
		k := key{s.strategyID, strings.ToUpper(s.symbol), day(s.asOf)}
		if prev, ok := best[k]; !ok || s.id > prev.id {
			best[k] = s
		}
	}
	out := make([]buySig, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	return out
}

func matchSignal(sigs []buySig, so stageRow) (buySig, bool) {
	var found buySig
	ok := false
	for _, s := range sigs {
		if s.stagedID == so.id {
			return s, true
		}
		if s.strategyID != so.strategy || !strings.EqualFold(s.symbol, so.symbol) {
			continue
		}
		if day(s.asOf) != "" && day(so.stagedAt) != "" && day(s.asOf) > day(so.stagedAt) {
			continue
		}
		if !ok || s.asOf > found.asOf || (s.asOf == found.asOf && s.id > found.id) {
			found, ok = s, true
		}
	}
	return found, ok
}

func fromStage(so stageRow, sig buySig, hasSig bool, ex *exitRow) ledger {
	row := ledger{
		account: so.account, strategy: so.strategy, symbol: strings.ToUpper(so.symbol),
		signalID: so.signalID, stageID: so.id, orderID: so.orderID,
		entryLimit: so.entry, qty: so.qty, hold: so.hold, tp: so.tp, sl: so.sl,
	}
	if hasSig {
		row.buySignalID = sig.id
		row.asOf = day(sig.asOf)
		row.next = day(sig.next)
		row.close = sig.close
		if sig.buyLimit > 0 {
			row.entryLimit = sig.buyLimit
		}
		if sig.takeProfit > 0 {
			row.tp = sig.takeProfit
		}
		if sig.stopLoss > 0 {
			row.sl = sig.stopLoss
		}
		if sig.holdDays > 0 {
			row.hold = sig.holdDays
		}
		if row.strategy == "" {
			row.strategy = sig.strategyID
		}
	}
	if row.asOf == "" {
		row.asOf = day(so.stagedAt)
	}
	if ex != nil {
		applyExit(&row, *ex)
	} else {
		row.liveOutcome, row.skipReason = stageOutcome(so.status, so.reason)
	}
	return row
}

func fromSignal(sig buySig) ledger {
	row := ledger{
		strategy: sig.strategyID, symbol: strings.ToUpper(sig.symbol), asOf: day(sig.asOf), next: day(sig.next),
		buySignalID: sig.id, entryLimit: sig.buyLimit, close: sig.close, tp: sig.takeProfit, sl: sig.stopLoss, hold: sig.holdDays,
	}
	if sig.buyLimit <= 0 {
		row.entryLimit = sig.close
	}
	switch sig.status {
	case "DISMISSED":
		row.liveOutcome = "SKIPPED"
		row.skipReason = sig.notes
	default:
		row.liveOutcome = "SIGNAL_ONLY"
		row.skipReason = sig.notes
	}
	return row
}

func fromExit(ex exitRow) ledger {
	row := ledger{account: ex.account, symbol: strings.ToUpper(ex.symbol)}
	applyExit(&row, ex)
	if row.strategy == "" {
		row.strategy = ex.strategy
	}
	if row.asOf == "" {
		row.asOf = day(ex.entryTime)
	}
	return row
}

func applyExit(row *ledger, ex exitRow) {
	row.exitID = ex.id
	row.account = ex.account
	if ex.strategyID != "" {
		row.strategy = ex.strategyID
	}
	if ex.asOf != "" {
		row.asOf = day(ex.asOf)
	}
	if ex.next != "" {
		row.next = day(ex.next)
	}
	if ex.buySignalID > 0 {
		row.buySignalID = ex.buySignalID
	}
	if ex.signalID > 0 {
		row.signalID = ex.signalID
	}
	if ex.stageID > 0 {
		row.stageID = ex.stageID
	}
	if ex.orderID != "" {
		row.orderID = ex.orderID
	}
	if ex.entryLimit > 0 {
		row.entryLimit = ex.entryLimit
	}
	row.entryFill = ex.entryFill
	row.exitFill = ex.exitFill
	row.fees = ex.fees
	if ex.filledQty > 0 {
		row.qty = ex.filledQty
	} else if ex.qty > 0 {
		row.qty = ex.qty
	}
	row.liveExitDate = day(ex.exitDate)
	row.liveReason = ex.reasonCode
	if row.liveReason == "" {
		row.liveReason = manage_exits.ExitReasonCode(ex.status, ex.exitReason)
	}
	row.liveOutcome = exitOutcome(ex.status)
	if ex.entryFill > 0 && ex.exitFill > 0 {
		row.liveReturn = (ex.exitFill - ex.entryFill) / ex.entryFill
	} else {
		row.liveReturn = ex.pnlPct / 100
	}
	if ex.hold > 0 {
		row.hold = ex.hold
	}
	fillDay := day(ex.filledAt)
	if fillDay == "" {
		fillDay = day(ex.entryTime)
	}
	if row.next != "" && fillDay != "" {
		row.delay = sql.NullFloat64{Float64: sessionDelay(row.next, fillDay), Valid: true}
	}
}

func stageOutcome(status, reason string) (string, string) {
	switch status {
	case stage_orders.StagedOrderRejected:
		return "SKIPPED", reason
	case stage_orders.StagedOrderCancelled:
		return "UNFILLED", reason
	case stage_orders.StagedOrderPending, stage_orders.StagedOrderSubmitting, stage_orders.StagedOrderPendingFill:
		return "PENDING", reason
	case stage_orders.StagedOrderSubmitted:
		return "SIGNAL_ONLY", "submitted with no manage_exit row"
	default:
		return "SIGNAL_ONLY", reason
	}
}

func exitOutcome(status string) string {
	switch status {
	case manage_exits.TradeStatusOpen:
		return "OPEN"
	case manage_exits.TradeStatusPendingFill:
		return "PENDING"
	case manage_exits.TradeStatusCancelled:
		return "UNFILLED"
	default:
		if strings.HasPrefix(status, "CLOSED") {
			return "CLOSED"
		}
		return status
	}
}

// applyModel replays each strategy over bars. A strategy that calculates its signals in SQL
// (every one but the row families' defaults) reads marketDB and writes its slice tables
// to a throwaway calc database; with no marketDB it has no signals and no row matches.
func applyModel(rows []ledger, marketDB string, bars map[string][]models.Bar) {
	calcDB := ""
	if marketDB != "" {
		if dir, err := os.MkdirTemp("", "live_vs_backtest_trades_calc"); err == nil {
			defer os.RemoveAll(dir)
			calcDB = filepath.Join(dir, "calc.db")
		}
	}
	fired := map[string]map[string]bool{}
	cfgs := map[string]strategy.StrategyConfig{}
	known := map[string]bool{}
	for i := range rows {
		id := rows[i].strategy
		if _, ok := known[id]; ok {
			continue
		}
		known[id] = true
		strat, ok := strategy.Get(id)
		if !ok {
			continue
		}
		cfgs[id] = strat.DefaultConfig()
		if calcDB != "" {
			strat.SetDatabases(marketDB, calcDB)
		}
		set := map[string]bool{}
		for _, sig := range strat.GenerateSignals(bars) {
			d := sig.Date
			if len(d) >= 10 {
				d = d[:10]
			}
			set[strings.ToUpper(sig.Symbol)+"|"+d] = true
		}
		fired[id] = set
	}
	for i := range rows {
		row := &rows[i]
		cfg, ok := cfgs[row.strategy]
		if !ok {
			row.modelOutcome = "NO_STRATEGY"
			continue
		}
		if fired[row.strategy][row.symbol+"|"+row.asOf] {
			row.signalMatch = 1
		}
		symBars := bars[row.symbol]
		if len(symBars) == 0 || row.asOf == "" {
			row.modelOutcome = hoursim.RoundTripNoBars
			continue
		}
		limit := row.entryLimit
		if limit <= 0 {
			limit = row.close
		}
		close := row.close
		if close <= 0 {
			close = limit
		}
		sig := models.Signal{
			Symbol: row.symbol, Date: row.asOf, Close: close, BuyLimit: limit,
			TakeProfit: row.tp, StopLoss: row.sl, HoldDaysOverride: row.hold,
			OrderType: "limit", StrategyID: row.strategy,
		}
		rt := hoursim.ReplayRoundTrip(sig, symBars, nil, cfg)
		row.modelOutcome = rt.Outcome
		row.modelEntry = rt.Trade.EntryPrice
		row.modelExit = rt.Trade.ExitPrice
		row.modelExitDate = rt.Trade.ExitDate
		row.modelReason = string(rt.Trade.ExitReason)
		row.modelReturn = rt.Trade.ReturnPct
		if bps, ok := SlippageBps(row.entryFill, row.modelEntry); ok {
			row.entryBps = sql.NullFloat64{Float64: bps, Valid: true}
		}
		if bps, ok := SlippageBps(row.exitFill, row.modelExit); ok {
			row.exitBps = sql.NullFloat64{Float64: bps, Valid: true}
		}
		if bps, ok := SlippageBps(row.entryFill, row.entryLimit); ok {
			row.limitBps = sql.NullFloat64{Float64: bps, Valid: true}
		}
		row.outcomeMatch = outcomeMatch(row.liveOutcome, row.modelOutcome, row.liveReason, row.modelReason)
	}
}

func outcomeMatch(live, model, liveReason, modelReason string) int {
	switch {
	case live == "UNFILLED" && model == hoursim.RoundTripUnfilled:
		return 1
	case live == "OPEN" && model == hoursim.RoundTripOpen:
		return 1
	case live == "CLOSED" && model == hoursim.RoundTripClosed && liveReason != "" && liveReason == modelReason:
		return 1
	default:
		return 0
	}
}

func sessionDelay(next, fill string) float64 {
	n, errN := time.Parse("2006-01-02", next)
	f, errF := time.Parse("2006-01-02", fill)
	if errN != nil || errF != nil {
		return 0
	}
	if f.Before(n) {
		return -float64(calendar.TradingDaysBetween(f, n))
	}
	return float64(calendar.TradingDaysBetween(n, f))
}

func day(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
