package runner

import (
	"sort"
	"strings"
	"sync"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/jmoiron/sqlx"
)

// DefaultBatchSize is how many strategies share one bar load in RunBatched.
const DefaultBatchSize = 200

// BatchOptions control RunBatched.
type BatchOptions struct {
	DB           *sqlx.DB
	Table        string
	Start, End   string // bar window passed to FetchBars
	SymbolFilter string // extra symbol every batch also loads (backtest -symbol)
	Workers      int
	BatchSize    int // strategies per bar load; 0 = DefaultBatchSize
}

// BatchRunFunc runs one strategy against the bars of its batch.
type BatchRunFunc func(s strategy.Strategy, bars map[string][]models.Bar, dates []string) RunResult

// RunBatched runs every strategy and returns the results in input order. Bars
// are loaded per batch, only for the symbols that batch's strategies declare,
// and released before the next batch, so memory is bounded by the batch rather
// than by every symbol in the market DB. A strategy that declares no symbols
// (a universe-wide pipeline) cannot be narrowed: those run together in one
// final batch that loads every symbol.
func RunBatched(opts BatchOptions, strats []strategy.Strategy, run BatchRunFunc) ([]RunResult, error) {
	results := make([]RunResult, len(strats))
	size := opts.BatchSize
	if size <= 0 {
		size = DefaultBatchSize
	}

	var scoped, universe []int
	for i, s := range strats {
		if p, ok := s.(strategy.RequiredSymbolsProvider); ok && len(p.RequiredSymbols()) > 0 {
			scoped = append(scoped, i)
		} else {
			universe = append(universe, i)
		}
	}
	// Order by id so a batch holds neighbours, which usually share symbols.
	sort.SliceStable(scoped, func(a, b int) bool {
		return strings.ToLower(strats[scoped[a]].ID()) < strings.ToLower(strats[scoped[b]].ID())
	})

	// One bar load per batch. The next scoped batch loads while the current one
	// runs, so the workers are not idle during a load. The universe batch loads
	// only when its turn comes: it holds every symbol.
	type batch struct {
		idx      []int
		symbols  []string
		prefetch bool
	}
	var batches []batch
	for from := 0; from < len(scoped); from += size {
		to := from + size
		if to > len(scoped) {
			to = len(scoped)
		}
		idx := scoped[from:to]
		group := make([]strategy.Strategy, len(idx))
		for k, i := range idx {
			group[k] = strats[i]
		}
		batches = append(batches, batch{idx: idx, symbols: RequiredSymbolsFor(group, opts.SymbolFilter), prefetch: true})
	}
	if len(universe) > 0 {
		batches = append(batches, batch{idx: universe})
	}

	type loaded struct {
		bars  map[string][]models.Bar
		dates []string
		err   error
	}
	load := func(b batch) <-chan loaded {
		ch := make(chan loaded, 1)
		go func() {
			bars, dates, err := storage.FetchBars(opts.DB, opts.Table, b.symbols, opts.Start, opts.End)
			ch <- loaded{bars, dates, err}
		}()
		return ch
	}
	runBatch := func(idx []int, l loaded) {
		workers := opts.Workers
		if workers < 1 {
			workers = 1
		}
		if workers > len(idx) {
			workers = len(idx)
		}
		jobs := make(chan int, len(idx))
		for _, i := range idx {
			jobs <- i
		}
		close(jobs)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					results[i] = run(strats[i], l.bars, l.dates)
				}
			}()
		}
		wg.Wait()
	}

	var next <-chan loaded
	if len(batches) > 0 {
		next = load(batches[0])
	}
	for i, b := range batches {
		cur := <-next
		if cur.err != nil {
			return results, cur.err
		}
		if i+1 < len(batches) && batches[i+1].prefetch {
			next = load(batches[i+1])
		} else if i+1 < len(batches) {
			next = nil // loaded when its turn comes
		}
		runBatch(b.idx, cur)
		cur = loaded{} // let the batch's bars go before the next one is held
		if i+1 < len(batches) && next == nil {
			next = load(batches[i+1])
		}
	}
	return results, nil
}

// DetailKept is how many strategies of a bulk run keep their trades and equity
// curve in memory (for the HTML report). Every other result keeps only its
// report and where it was saved: the detail is in the family result database.
const DetailKept = 100

// Slim returns r without the trades, equity curve and notes, which are the part
// of a result that grows with the length of the backtest. A bulk run of tens of
// thousands of strategies would otherwise hold every curve until it finished.
func (r RunResult) Slim() RunResult {
	r.Trades, r.EquityCurve, r.Notes = nil, nil, nil
	return r
}

// TopDetail remembers the full result of the k best strategies by CAGR as a run
// goes, and hands every result back slim. It is safe for concurrent workers.
type TopDetail struct {
	mu   sync.Mutex
	k    int
	kept map[string]RunResult
}

// NewTopDetail keeps the detail of the k best results; k <= 0 keeps none.
func NewTopDetail(k int) *TopDetail { return &TopDetail{k: k, kept: map[string]RunResult{}} }

// Offer stores r when it is among the best k so far (dropping the weakest it
// displaces) and returns the slim result to keep in the run's result list.
func (t *TopDetail) Offer(r RunResult) RunResult {
	if t.k > 0 && r.Err == nil && r.Strat != nil {
		t.mu.Lock()
		if len(t.kept) < t.k {
			t.kept[r.Strat.ID()] = r
		} else {
			worstID, worst := "", 0.0
			for id, kr := range t.kept {
				if worstID == "" || kr.Report.CAGR < worst {
					worstID, worst = id, kr.Report.CAGR
				}
			}
			if r.Report.CAGR > worst {
				delete(t.kept, worstID)
				t.kept[r.Strat.ID()] = r
			}
		}
		t.mu.Unlock()
	}
	return r.Slim()
}

// Detail returns the full result kept for a strategy id, if it is among the best.
func (t *TopDetail) Detail(id string) (RunResult, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.kept[id]
	return r, ok
}
