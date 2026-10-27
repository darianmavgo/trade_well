package scan_market

import (
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
)

// Scan statuses of one strategy in a scan_market report.
const (
	ScanBuy        = "BUY"         // the strategy entered on the as-of bar
	ScanNoBuy      = "NO_BUY"      // it was scanned and did not enter
	ScanNotScanned = "NOT_SCANNED" // livescan returned nothing for it
)

// maxListed caps how many BUY signals one report row spells out (a rotation can have dozens).
const maxListed = 5

// StrategyScan is what one scan did for one strategy: the symbols it read, the verdict and why.
// A report lists every strategy that was scanned, not only the ones that fired.
type StrategyScan struct {
	StrategyID string
	Symbols    string // the symbols the strategy reads, comma separated
	Status     string // ScanBuy, ScanNoBuy or ScanNotScanned
	Detail     string
}

// EmailRow is the scan_market report line for this strategy.
func (s StrategyScan) EmailRow() send_email.Row {
	status := "NO_SIGNAL"
	switch s.Status {
	case ScanBuy:
		status = "SIGNAL"
	case ScanNotScanned:
		status = "ERROR"
	}
	return send_email.Row{Account: "all", Strategy: s.StrategyID, Symbol: s.Symbols, Signal: s.Status, Status: status, Detail: s.Detail}
}

// EmailRows is the scan_market report table: one row per scanned strategy.
func EmailRows(scans []StrategyScan) []send_email.Row {
	rows := make([]send_email.Row, len(scans))
	for i, s := range scans {
		rows[i] = s.EmailRow()
	}
	return rows
}

// buyDetail spells out a strategy's BUY signals, the first few in full.
func buyDetail(buys []Signal) string {
	parts := make([]string, 0, maxListed)
	for i, b := range buys {
		if i == maxListed {
			parts = append(parts, fmt.Sprintf("+%d more", len(buys)-maxListed))
			break
		}
		parts = append(parts, fmt.Sprintf("%s @ %.4f TP $%.4f SL $%.4f alloc %.0f%%", b.Symbol, b.Price, b.Raw.TakeProfit, b.Raw.StopLoss, b.AllocPct*100))
	}
	return fmt.Sprintf("%d BUY: %s", len(buys), strings.Join(parts, "; "))
}

// StrategyScans reports every strategy this Run scanned: those that fired (BUY) and those that
// did not (NO_BUY, with livescan's reason).
func (r *Result) StrategyScans() []StrategyScan {
	var out []StrategyScan
	fired := map[string][]Signal{}
	var order []string
	for _, s := range r.Signals {
		if _, ok := fired[s.StrategyID]; !ok {
			order = append(order, s.StrategyID)
		}
		fired[s.StrategyID] = append(fired[s.StrategyID], s)
	}
	for _, id := range order {
		syms := make([]string, 0, len(fired[id]))
		for _, s := range fired[id] {
			syms = append(syms, s.Symbol)
		}
		out = append(out, StrategyScan{StrategyID: id, Symbols: strings.Join(syms, ","), Status: ScanBuy, Detail: buyDetail(fired[id])})
	}
	for _, n := range r.NoBuy {
		out = append(out, StrategyScan{StrategyID: n.StrategyID, Symbols: strings.Join(n.Symbols, ","), Status: ScanNoBuy, Detail: n.Detail})
	}
	return out
}
