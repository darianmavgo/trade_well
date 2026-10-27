package runner

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/datasource"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/sessions"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// RunDownload fetches missing hourly bars for symbols into targetTable of the
// SQLite DB at targetDb, covering the last `years` years through today, from
// Alpaca (ALPACA_KEY / ALPACA_SECRET), and then derives the session rows that
// signals read (see pkg/sessions). It is cmd/market_history's missing-window loop
// as a plain function -- only date windows not already cached are fetched --
// so callers can refresh market data in-process instead of exec'ing a
// separately built `market_history` binary (or `go run ./cmd/market_history`), which can't
// exist on a deployment target like App Engine.
//
// A symbol whose fetch fails is only an error when it has no cached bars at
// all (nothing to scan); a failed incremental top-up of an already-cached
// symbol is tolerated, and a genuinely stale tip is still refused downstream
// by ResolveLiveAsOf.
func RunDownload(targetDb, targetTable string, symbols []string, years int) error {
	return RunDownloadContext(context.Background(), targetDb, targetTable, symbols, years)
}

// DailyBarSource, when set, replaces the Alpaca hourly download: it returns up to
// `years` of finished daily session bars ("YYYY-MM-DD", timeframe 1d, oldest first)
// for one symbol. Signals read only those daily rows, so a deployment whose live
// broker already serves daily history (the orchestrator's Schwab client) can refresh
// the market DB without Alpaca keys or hourly bars.
var DailyBarSource func(ctx context.Context, symbol string, years int) ([]models.Bar, error)

// runDailyDownload refreshes every symbol from DailyBarSource. Every symbol must
// succeed: a stale or missing symbol is refused here rather than scanned.
func runDailyDownload(ctx context.Context, targetDb, targetTable string, symbols []string, years int) error {
	db, err := storage.OpenSQLite(targetDb)
	if err != nil {
		return fmt.Errorf("open %s: %w", targetDb, err)
	}
	defer db.Close()
	if err := storage.EnsureBarTable(db, targetTable); err != nil {
		return fmt.Errorf("ensure table %s: %w", targetTable, err)
	}
	for _, sym := range symbols {
		if err := ctx.Err(); err != nil {
			return err
		}
		bars, err := DailyBarSource(ctx, sym, years)
		if err != nil {
			return fmt.Errorf("%s: daily history: %w", sym, err)
		}
		if err := storage.UpsertBars(db, targetTable, bars); err != nil {
			return fmt.Errorf("%s: save bars: %w", sym, err)
		}
	}
	return nil
}

// RunDownloadContext is RunDownload with cancellation: once ctx is done no
// further symbols are started, in-flight fetches are aborted, and ctx's error
// is returned.
func RunDownloadContext(ctx context.Context, targetDb, targetTable string, symbols []string, years int) error {
	if len(symbols) == 0 {
		return nil
	}
	if targetTable == "" {
		targetTable = "backtest_start"
	}
	if DailyBarSource != nil {
		return runDailyDownload(ctx, targetDb, targetTable, symbols, years)
	}

	db, err := storage.OpenSQLite(targetDb)
	if err != nil {
		return fmt.Errorf("open %s: %w", targetDb, err)
	}
	defer db.Close()
	if err := storage.EnsureBarTable(db, targetTable); err != nil {
		return fmt.Errorf("ensure table %s: %w", targetTable, err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	source := datasource.NewAlpacaDataSource("", "", client)

	now := time.Now().UTC()
	start := now.AddDate(-years, 0, 0)
	startStr, endStr := start.Format("2006-01-02"), now.Format("2006-01-02")

	var (
		mu       sync.Mutex // serializes all DB access and firstErr (SQLite is single-writer)
		firstErr error
		wg       sync.WaitGroup
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	sem := make(chan struct{}, 4) // network-bound; modest fan-out

	for _, sym := range symbols {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(sym string) {
			defer wg.Done()
			defer func() { <-sem }()

			mu.Lock()
			cov, covErr := storage.GetSymbolDateCoverageWithTimeframe(db, targetTable, sym, "1h")
			mu.Unlock()
			if covErr != nil {
				fail(fmt.Errorf("%s: coverage check: %w", sym, covErr))
				return
			}

			var windows []datasource.FetchRequest
			if cov.BarCount == 0 {
				windows = append(windows, datasource.FetchRequest{Symbol: sym, StartDate: start, EndDate: now, Timeframe: "1h"})
			} else {
				if startStr < cov.MinDate {
					if dbMin, perr := time.Parse("2006-01-02", cov.MinDate); perr == nil && dbMin.Sub(start) > 4*24*time.Hour {
						windows = append(windows, datasource.FetchRequest{Symbol: sym, StartDate: start, EndDate: dbMin, Timeframe: "1h"})
					}
				}
				if cov.MaxDate < endStr {
					if dbMax, perr := time.Parse("2006-01-02", cov.MaxDate); perr == nil {
						windows = append(windows, datasource.FetchRequest{Symbol: sym, StartDate: dbMax, EndDate: now, Timeframe: "1h"})
					}
				}
			}

			for _, win := range windows {
				bars, ferr := source.Fetch(ctx, win)
				if ferr != nil {
					if cov.BarCount == 0 {
						fail(fmt.Errorf("%s: fetch %s..%s: %w", sym, win.StartDate.Format("2006-01-02"), win.EndDate.Format("2006-01-02"), ferr))
					} else {
						// Tolerated (the cached bars stay usable), but say so: a stale
						// tip otherwise surfaces only as STALE_MARKET_DATA downstream.
						log.Printf("[download] %s: top-up %s..%s failed, keeping cached bars through %s: %v",
							sym, win.StartDate.Format("2006-01-02"), win.EndDate.Format("2006-01-02"), cov.MaxDate, ferr)
					}
					continue
				}
				if len(bars) == 0 {
					continue
				}
				mu.Lock()
				uerr := storage.UpsertBars(db, targetTable, bars)
				mu.Unlock()
				if uerr != nil {
					fail(fmt.Errorf("%s: save bars: %w", sym, uerr))
				}
			}
		}(sym)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := sessions.Derive(db, targetTable); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("derive sessions: %w", err)
	}
	return firstErr
}
