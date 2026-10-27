package strategy

import (
	"log"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// barWindow is the first and last bar date (YYYY-MM-DD) across every symbol in
// bars: the window the run was loaded for. A pipeline restricts itself to it so
// it sees the same bars a Go loop over bars would.
func barWindow(bars map[string][]models.Bar) (start, end string) {
	start, end = "9999-12-31", "0000-00-00"
	for _, bs := range bars {
		if len(bs) == 0 {
			continue
		}
		first, last := bs[0].Date, bs[len(bs)-1].Date
		if len(first) > 10 {
			first = first[:10]
		}
		if len(last) > 10 {
			last = last[:10]
		}
		if first < start {
			start = first
		}
		if last > end {
			end = last
		}
	}
	if end == "0000-00-00" {
		return "0000-00-00", "9999-12-31"
	}
	return start, end
}

// RunPipeline runs the SQL pipeline directory dir for one strategy and returns
// its signals stamped with the strategy id (and orderType where the pipeline
// left it empty). The pipeline reads the market database and writes its slice
// tables to the calc database. START_DATE and END_DATE (the window of bars) are
// available to it as __START_DATE__ and __END_DATE__, with the strategy's own
// cfg.SQLParams. With either database path empty there is nothing to calculate
// from, so it logs and returns nil.
func RunPipeline(id, name, desc, dir string, cfg StrategyConfig, marketDB, calcDB, orderType string, bars map[string][]models.Bar) []models.Signal {
	if marketDB == "" || calcDB == "" {
		log.Printf("[%s] market and calc database paths are not set, no signals", id)
		return nil
	}
	params := make(map[string]string, len(cfg.SQLParams)+2)
	for k, v := range cfg.SQLParams {
		params[k] = v
	}
	params["START_DATE"], params["END_DATE"] = barWindow(bars)
	cfg.SQLParams = params

	pipe := NewSQLPipeline(id+"-run", name, desc, dir, cfg)
	pipe.SetDatabases(marketDB, calcDB)
	sigs := pipe.GenerateSignals(bars)
	for i := range sigs {
		sigs[i].StrategyID = id
		if sigs[i].OrderType == "" {
			sigs[i].OrderType = orderType
		}
	}
	return sigs
}
