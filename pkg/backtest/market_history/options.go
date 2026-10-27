package market_history

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/datasource"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/options"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

func parseOTMList(s string) ([]float64, error) {
	var out []float64
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return nil, fmt.Errorf("bad -otm value %q: %w", p, err)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("-otm needs at least one percentage, e.g. 0,2,5")
	}
	return out, nil
}

// downloadOptionHistory pulls monthly call-chain history for one underlying
// (Polygon free tier: 5 calls/min, ~2 years, EOD only) into option_contracts /
// option_bars in the market DB.
//
// For each monthly expiry E it lists that chain once, keeps only the strikes a
// covered-call roll would pick (nearest to spot*(1+otm%) at the prior monthly
// expiry), and pulls bars from that roll date to E. Anything already stored is
// skipped, so reruns and interrupted runs cost no calls for finished work.
func downloadOptionHistory(ctx context.Context, out io.Writer, db *sqlx.DB, barTable, underlying string, start, end time.Time,
	otms []float64, apiKey string, callsPerMin, maxCalls int) error {

	if err := storage.EnsureOptionTables(db); err != nil {
		return err
	}
	bySym, _, err := storage.FetchBars(db, barTable, []string{underlying}, "1900-01-01", "2100-01-01")
	if err != nil {
		return err
	}
	closes := bySym[underlying]
	if len(closes) == 0 {
		return fmt.Errorf("no %s bars in %s; run `market_history -symbols %s` first (option strikes are chosen from the underlying price)", underlying, barTable, underlying)
	}
	sort.Slice(closes, func(i, j int) bool { return closes[i].Date < closes[j].Date })
	// refClose returns the close and date of the last trading day on/before date
	// (the nominal roll Friday can be a market holiday, e.g. Juneteenth).
	refClose := func(date string) (float64, string, bool) {
		px, d, ok := 0.0, "", false
		for _, b := range closes {
			if b.Date[:10] > date {
				break
			}
			px, d, ok = b.Close, b.Date[:10], true
		}
		return px, d, ok
	}

	cli := datasource.NewPolygonOptions(apiKey, callsPerMin)
	if cli.APIKey == "" {
		return fmt.Errorf("Polygon API key is required. Pass -polygon-key <KEY> or set POLYGON_API_KEY in your environment / .env file")
	}
	today := time.Now().UTC().Format("2006-01-02")
	budget := func() bool { return maxCalls > 0 && cli.Calls >= maxCalls }

	expiries := options.MonthlyExpiries(start, end)
	fmt.Fprintf(out, "Option history for %s: %d monthly expiries %s ➔ %s, strikes nearest OTM %v%%, %d calls/min\n",
		underlying, len(expiries), start.Format("2006-01-02"), end.Format("2006-01-02"), otms, callsPerMin)

	var newContracts, newBars, cached, noQuote int
	for i, nominal := range expiries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if budget() {
			fmt.Fprintf(out, "⏸  Stopped at -max-calls %d (rerun to continue; finished work is skipped).\n", maxCalls)
			break
		}
		sell := options.PrevMonthly(nominal).Format("2006-01-02")
		if sell < start.Format("2006-01-02") {
			continue
		}
		ref, sellDay, ok := refClose(sell)
		if !ok {
			continue
		}
		sell = sellDay

		// Resolve the real expiry: the nominal third Friday, or Thursday when Friday is a holiday.
		var chain []models.OptionContract
		expiry := ""
		for _, cand := range []time.Time{nominal, nominal.AddDate(0, 0, -1)} {
			ex := cand.Format("2006-01-02")
			scanned, err := storage.ExpiryScanned(db, underlying, ex)
			if err != nil {
				return err
			}
			if !scanned {
				list, err := cli.ListContracts(ctx, underlying, ex, "call", ex < today)
				if err != nil {
					return fmt.Errorf("list %s %s: %w", underlying, ex, err)
				}
				if err := storage.UpsertOptionContracts(db, list); err != nil {
					return err
				}
				if err := storage.SaveExpiryScan(db, underlying, ex, len(list)); err != nil {
					return err
				}
				newContracts += len(list)
			}
			if err := db.Select(&chain, `SELECT ticker, underlying, expiry, strike, contract_type, shares_per_contract
				FROM option_contracts WHERE underlying = ? AND expiry = ? AND contract_type = 'call' ORDER BY strike`, underlying, ex); err != nil {
				return err
			}
			if len(chain) > 0 {
				expiry = ex
				break
			}
		}
		if expiry == "" {
			fmt.Fprintf(out, "[%d/%d] %s : no listed calls\n", i+1, len(expiries), nominal.Format("2006-01-02"))
			continue
		}

		strikes := make([]float64, len(chain))
		byStrike := map[float64]models.OptionContract{}
		for j, c := range chain {
			strikes[j] = c.Strike
			byStrike[c.Strike] = c
		}
		picked := map[float64]bool{}
		for _, o := range otms {
			if k, ok := options.NearestStrike(strikes, ref, o); ok {
				picked[k] = true
			}
		}
		var ks []float64
		for k := range picked {
			ks = append(ks, k)
		}
		sort.Float64s(ks)

		for _, k := range ks {
			c := byStrike[k]
			fetched, lastBar, err := storage.ContractFetchState(db, c.Ticker)
			if err != nil {
				return err
			}
			if fetched && expiry < today {
				cached++
				continue
			}
			if budget() {
				break
			}
			from := sell
			if lastBar != "" && lastBar > from {
				from = lastBar
			}
			to := expiry
			if to > today {
				to = today
			}
			bars, err := cli.Bars(ctx, c.Ticker, from, to)
			if err != nil {
				return fmt.Errorf("bars %s: %w", c.Ticker, err)
			}
			if err := storage.UpsertOptionBars(db, c.Ticker, bars); err != nil {
				return err
			}
			newBars += len(bars)
			if len(bars) == 0 {
				noQuote++
			}
			fmt.Fprintf(out, "[%d/%d] %s  %s K=%.2f (spot %.2f @ %s): %d bars\n", i+1, len(expiries), expiry, c.Ticker, k, ref, sell, len(bars))
		}
	}

	fmt.Fprintf(out, "\n✨ Options summary: %d contracts listed, %d bars added, %d contracts already cached, %d never traded in window, %d API calls.\n",
		newContracts, newBars, cached, noQuote, cli.Calls)
	return nil
}

// downloadAlpacaOptions pulls every listed call and put (expired ones too) for
// each underlying, and their daily bars, into option_contracts / option_bars in
// the market DB. Alpaca's option history starts in early 2024. Contracts whose
// bars were pulled and have expired are skipped, so reruns only add new work.
func downloadAlpacaOptions(ctx context.Context, out io.Writer, db *sqlx.DB, underlyings []string, start, end time.Time, key, secret string) error {
	if err := storage.EnsureOptionTables(db); err != nil {
		return err
	}
	cli := datasource.NewAlpacaOptions(key, secret)
	today := time.Now().UTC().Format("2006-01-02")
	from, to := start.Format("2006-01-02"), end.Format("2006-01-02")
	const batch = 100
	// Alpaca refuses an end at or after today ("OPRA agreement is not signed":
	// the newest quotes are not on this plan), but serves the delayed bars when no end is sent.
	barsTo := to
	if to >= today {
		barsTo = ""
	}
	for _, u := range underlyings {
		contracts, err := cli.ListContracts(ctx, u, from)
		if err != nil {
			return fmt.Errorf("list %s contracts: %w", u, err)
		}
		if err := storage.UpsertOptionContracts(db, contracts); err != nil {
			return err
		}
		var todo []string
		for _, c := range contracts {
			if c.Expiry < from {
				continue
			}
			fetched, _, err := storage.ContractFetchState(db, c.Ticker)
			if err != nil {
				return err
			}
			if fetched && c.Expiry < today {
				continue
			}
			todo = append(todo, c.Ticker)
		}
		fmt.Fprintf(out, "Alpaca options %s: %d contracts listed, %d to fetch bars for (%s ➔ %s)\n", u, len(contracts), len(todo), from, to)
		var bars int
		for i := 0; i < len(todo); i += batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			chunk := todo[i:min(i+batch, len(todo))]
			got, err := cli.Bars(ctx, chunk, from, barsTo)
			if err != nil {
				return fmt.Errorf("bars %s: %w", u, err)
			}
			for _, t := range chunk {
				if err := storage.UpsertOptionBars(db, t, got[t]); err != nil {
					return err
				}
				bars += len(got[t])
			}
			fmt.Fprintf(out, "  %s: %d/%d contracts, %d bars so far\n", u, min(i+batch, len(todo)), len(todo), bars)
		}
	}
	fmt.Fprintf(out, "\n✨ Alpaca options done: %d API calls.\n", cli.Calls)
	return nil
}
