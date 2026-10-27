package runner

import (
	"fmt"
	"runtime"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// DefaultBarsTable is the historical-bars table every entrypoint (backtest,
// livescan, embedding callers) reads from by default.
const DefaultBarsTable = "backtest_start"

// RunLiveScan runs the live signal-scan phase in-process with the standard
// defaults: DefaultBarsTable, one worker per CPU, and AutoDownload on, so a
// fresh or empty marketDB is created and filled for the strategies' symbols
// (the configured market data source) before scanning. A stale tip
// is still refused via ResolveLiveAsOf. outDir, if set, receives
// livescan.db. It is the entrypoint for callers that embed livescan instead
// of shelling out to the cmd/livescan binary.
func RunLiveScan(strats []strategy.Strategy, marketDB, outDir string) (*SignalScanResult, error) {
	if len(strats) == 0 {
		return nil, fmt.Errorf("livescan: no strategies")
	}
	res, err := RunSignalScan(SignalScanOptions{
		MarketDB:     marketDB,
		Table:        DefaultBarsTable,
		Strategies:   strats,
		AutoDownload: true,
		Concurrency:  runtime.NumCPU(),
		OutDir:       outDir,
	})
	if err != nil {
		return nil, fmt.Errorf("LIVESCAN_FAILED: %w", err)
	}
	if res.AsOf == "" {
		return nil, fmt.Errorf("LIVESCAN_FAILED: empty as_of in scan result")
	}
	return res, nil
}
