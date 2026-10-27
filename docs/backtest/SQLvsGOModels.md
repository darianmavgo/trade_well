# SQL vs Go Models — Architectural Decision Guide

> *Context: `backtestgosqlite` — a financial backtesting engine where price data, signals,
> and strategy calculations live in SQLite, while orchestration, I/O, and domain types
> live in Go.*

---

## Background: The Journey Here

This project's architecture was shaped by three painful iterations:

| Phase | Stack | Pain Point |
|-------|-------|-----------|
| 1 | Python + NumPy/pandas | Performance — large rolling-window calculations on thousands of symbols were too slow; GIL contention, memory bloat, startup overhead |
| 2 | Pure Go calculation | Speed improved dramatically, but Go's strict nil-safety meant constant nil pointer dereferences on optional fields, verbose pointer-of-pointer patterns, and complex null-handling boilerplate for every optional metric |
| 3 | Go orchestration + SQLite math | SQL window functions, multi-table JOINs, and `INSERT INTO … SELECT` pipelines handle every calculation natively; Go owns only I/O, types, and wiring |

The current hybrid is not a compromise — it is the natural resting point where each layer
does what it is genuinely best at.

---

## Part 1 — Models Defined in Go

Go models in this project live in `pkg/backtest/models/models.go`.
They define the domain types that cross the Go/SQLite boundary: `Bar`, `Signal`, `Trade`,
`Position`, `Account`, `DailyEquityPoint`, `PerformanceReport`.

### ✅ Pros of Go-Defined Models

#### Type Safety at Compile Time
Go structs give you compile-time guarantees that the field exists, the type is correct, and
the tag matches. Mismatches between your code and a query result surface at build time (with
`sqlx` scanning) or in tests — not in production data.

```go
// Compiler enforces this field exists and is float64
type Trade struct {
    EntryPrice float64 `json:"entry_price"`
    ExitPrice  float64 `json:"exit_price"`
    ReturnPct  float64 `json:"return_pct"`
}
```

#### IDE Autocompletion and Refactoring
Renaming `ReturnPct` across every caller is a single rename refactor. In raw SQL strings,
you grep and pray.

#### Canonical Single Source of Truth for the API Layer
The `PerformanceReport` struct with its 30+ fields drives the JSON API, the web UI, and the
report renderer — all from one definition. If the struct changes, every consumer is
type-checked immediately.

#### Testability of Business Logic
Go unit tests can construct a `Trade` or `Account` value in memory without a database.
Validation logic, commission calculation, exit-reason classification — all testable in
microseconds with no SQLite file required.

#### Expressive Domain Semantics
Typed constants like `ExitReason` communicate intent that SQL `TEXT` columns never can:

```go
type ExitReason string

const (
    ExitReasonProfitTarget ExitReason = "PROFIT_TARGET"
    ExitReasonStopLoss     ExitReason = "STOP_LOSS"
    ExitReasonTrailingStop ExitReason = "TRAILING_STOP"
)
```

An `ExitReason` field in a struct is self-documenting and exhaustively checkable with a
`switch` statement. A raw string in a database cell is neither.

#### Portability
Go structs are database-agnostic. Swapping SQLite for DuckDB, Postgres, or an in-memory
store requires only changing the driver and query layer — the domain model is untouched.

---

### ❌ Cons of Go-Defined Models

#### Nil Pointer Pain (The Reason Phase 2 Was Abandoned)
The moment a field is optional — an entry that may or may not have triggered, a stop-loss
that may not have been set — Go forces you into pointer types:

```go
// The boilerplate tax for "this might not exist"
type Signal struct {
    StopLoss   *float64 `json:"stop_loss,omitempty"`
    TakeProfit *float64 `json:"take_profit,omitempty"`
    Metadata   map[string]float64 `json:"metadata,omitempty"`
}

// And then at every call site:
if sig.StopLoss != nil {
    risk = entryPrice - *sig.StopLoss
}
```

For financial models with dozens of optional metrics, this overhead dominates the code.
Every dereference is a potential runtime panic if discipline lapses.

#### Rolling Window Math Is Verbose and Error-Prone
Computing a 10-day rolling maximum high across thousands of symbols in Go requires:
- An explicit loop over bars, per symbol
- A sliding buffer or sorted structure
- Boundary checks for the first N bars
- Memory allocation for intermediate results
- Careful handling of symbol transitions

The equivalent in SQL is five lines (see `sql/02_pipeline/08_calc_maxhigh10.sql`):

```sql
SELECT d.idx,
    max(d1.high, d2.high, ..., d10.high) AS maxhigh10
FROM backtest_start AS d
JOIN backtest_start AS d1 ON d1.idx = d.idx + 1 AND d1.symbol = d.symbol
...
```

#### Performance on Set Operations
Go loops are sequential by default. Set-based operations across 50,000+ rows — filtering,
aggregation, multi-symbol window calculations — are where SQLite's query planner,
B-tree indexes, and compiled bytecode consistently outperform hand-written Go iteration.

#### Schema Drift
When the SQLite schema adds a column, the Go struct must be manually updated to match.
Forgetting one field causes silent data loss (the column is ignored during scanning) or a
runtime error. There is no foreign key enforcement between a struct tag and a column name.

---

## Part 2 — Models Defined in SQL

The SQL pipeline in `sql/02_pipeline/` defines every calculation as a chain of
materialized intermediate tables:

```
backtest_start
  → minlow4_slice        (01_calc_minlow_all.sql)
  → wc_trailing_minlow   (02_wc_calc_trailing_minlow.sql)
  → minlow4_slice        (03_calc_minlow4.sql)
  → wc_buy_signal_slice  (04_wc_buy_signal_slice.sql)
  → wc_entry_slice       (05_calc_wc_entry_slice.sql)
  → entry                (06_calc_entry.sql)
  → maxhigh4_slice       (07_calc_maxhigh4.sql)
  → maxhigh10_slice      (08_calc_maxhigh10.sql)
  → win_slice            (09_calc_win_slice.sql)
  → win20_10d_slice      (10_calc_win20_10d.sql)
  → wc_backtest_details  (11_wc_backtest_details.sql)
  → wc_summary           (12_wc_summary.sql)
  → filtered symbols     (13_filter_90pct_symbols.sql)
```

### ✅ Pros of SQL-Defined Models

#### Set-Based Computation Is SQLite's Native Mode
SQL operates on entire result sets at once. A 10-table JOIN that computes 10-day forward
windows for every symbol in a single `INSERT INTO … SELECT` is not a trick — it is the
paradigm SQL was designed for. SQLite's query planner optimizes join order, uses indexes,
and avoids materializing intermediate row-by-row state.

#### Intermediate Results Are Inspectable
Every `CREATE TABLE AS SELECT` step produces a real table you can query with any SQL
client. Debugging a bad calculation means:

```sql
SELECT * FROM maxhigh10_slice WHERE idx = 42;
```

There is no debugger, no breakpoint, no print statement needed. The data is right there.

#### Composable Pipeline With No Glue Code
The numbered pipeline (`01_` → `13_`) is entirely self-documenting. Each step reads from
the previous step's table and writes to the next. Adding a new calculation step means
adding one SQL file — no Go code changes, no struct additions, no recompilation.

#### Window Math Is Terse and Correct by Default
SQL's `MIN()`, `MAX()`, `SUM()`, `AVG()` aggregate functions handle NULLs correctly and
operate over arbitrarily large sets without you writing a single loop. The 4-day minimum
low across 4 self-joins in `01_calc_minlow_all.sql` would take 30+ lines of Go with
buffer management.

#### No Nil Pointers. Ever.
SQL has `NULL`, which propagates correctly through arithmetic (`NULL + anything = NULL`),
aggregates (ignored by default), and conditionals (`COALESCE`, `NULLIF`). There are no
runtime panics, no pointer dereferences, no option unwrapping ceremony.

#### Reproducibility and Auditability
A SQL pipeline is a complete, reproducible audit trail. Given the same `backtest_start`
data, re-running the numbered scripts in order produces byte-identical results every time.
This is essential for financial backtesting where reproducibility is a regulatory and
research requirement.

#### Performance for Bulk Analytics
SQLite reads data from disk in pages, processes it in compiled bytecode, and uses B-tree
indexes for join optimization. For the pattern this codebase uses — scanning tens of
thousands of price bars, computing rolling windows, aggregating per-symbol — SQLite
reliably outperforms equivalent Go loops by 2–10x on large datasets, with zero memory
allocation overhead in the Go process.

---

### ❌ Cons of SQL-Defined Models

#### No Compile-Time Checking
A typo in a column name (`buylimit` vs `buy_limit`) surfaces only at runtime, as a query
error or — worse — a NULL column silently populated with zeros in the output.

#### Limited Expressiveness for Complex Control Flow
SQL cannot express stateful iteration, early exits, or recursive logic (without CTEs that
become unreadable quickly). Go is simply better for: simulation loops that must react to
each new bar, broker execution logic that carries state between days, dynamic position
sizing that reads account equity mid-loop.

#### Schema Management Is Manual
`DROP TABLE IF EXISTS` / `CREATE TABLE AS SELECT` is powerful but fragile. There is no
migration framework, no foreign key enforcement between pipeline steps, and no guarantee
that step 8 references a column that step 7 actually produced — until you run it.

#### Harder to Unit Test in Isolation
Testing a single SQL calculation requires either a test database file or an in-memory
SQLite instance populated with fixture data. There is no equivalent of Go's `testing.T`
with zero-setup struct construction.

#### Tooling and IDE Support Is Weaker
SQL string literals in Go code get no syntax highlighting, no autocompletion, and no
rename refactoring. Even with `.sql` files, IDE support for SQLite-specific syntax
(e.g., multi-argument `min(a,b,c)`) is inconsistent.

#### Debugging Distributed Failures Is Harder
When a pipeline step silently produces wrong results (not an error — just wrong math),
tracking down which JOIN produced the bad intermediate value requires manually querying
each intermediate table. A Go debugger with a live call stack is more ergonomic for this
kind of investigation.

---

## Part 3 — Consequences of Moving All Models and Math to SQLite

This section addresses the specific architectural question: **what happens if you go all-in
and define every model, every calculation, and every business rule inside SQLite?**

### What "All Math in SQLite" Means in Practice

In this codebase, "all math in SQLite" is already largely true for the analytics pipeline.
The hypothetical full migration would additionally move:

- `Account` state tracking (cash, equity, drawdown) into SQL tables updated via `UPDATE`
- Position lifecycle (open, update trailing stop, close) into SQL `INSERT`/`UPDATE`/`DELETE`
- Trade settlement, commission calculation, and P&L into SQL arithmetic expressions
- Performance metric computation (`SharpeRatio`, `CAGR`, `MaxDrawdown`) into SQL aggregates
- The simulation loop itself into SQL triggers or a CTE-driven recursive query

---

### ✅ Positive Consequences

#### Maximum Throughput for Batch Backtests
If your backtest is entirely set-based — "compute all signals, then compute all exits, then
aggregate" — a pure SQL pipeline can process years of multi-symbol data in seconds. There
is no Python overhead, no Go loop, no row-by-row allocation. SQLite processes the full
dataset as a single query plan.

#### Single Artifact = Complete Reproducibility
A self-contained `.sql` pipeline + a `.db` file is a complete, shareable, reproducible
backtest artifact. Anyone with SQLite can reproduce your results exactly, with no Go
toolchain, no dependency graph, no version pinning required.

#### Eliminates the Go↔SQLite Impedance Mismatch
No scanning structs, no tag alignment, no `sqlx.StructScan`. Data stays in SQLite from
ingestion through analysis through reporting. The only time Go touches data is at the UI
or API boundary.

#### SQL Aggregates Handle Large Datasets Gracefully
Computing `MAX()`, variance-based `STDDEV()`, Sharpe ratio, and drawdown over 100,000
trades is a single query. Equivalent Go code requires multiple passes over the data,
careful memory management, and significant boilerplate.

#### Simpler Concurrency Model
SQLite's WAL (write-ahead log) mode handles concurrent readers with a single writer. You
avoid Go channel synchronization, mutex guards on shared account state, and race conditions
in the simulation loop — because there is no shared mutable Go memory.

---

### ⚠️ Negative Consequences (or What Requires Careful Management)

#### The Simulation Loop Cannot Be Pure SQL
A realistic backtest is **not** a pure set operation. The broker simulator needs to:

1. Consume each new bar in chronological order
2. Check if open orders are filled at today's open/high/low
3. Update trailing stops based on today's price action
4. Decide whether to open new positions given current cash
5. Carry forward account equity to tomorrow

This is stateful, ordered, conditional iteration. SQLite has no native loop construct.
Emulating it with recursive CTEs or triggers is technically possible but produces SQL that
is unmaintainable, undebuggable, and often slower than the Go equivalent because it defeats
the query planner's set-based optimization assumptions.

> **The boundary:** Use SQL for *what happened* (analytics, aggregation, window math).
> Use Go for *what to do next* (simulation, decision-making, stateful control flow).

#### Triggers Are a Maintenance Trap
Moving position management into SQLite triggers creates invisible side effects. A trigger
that updates `account.cash` when a `trades` row is inserted is powerful — and completely
invisible to anyone reading the Go code that fires the insert. Debugging cascading trigger
behavior in production is notoriously painful.

#### Numeric Precision Requires Even More Discipline
SQLite stores all floating-point as IEEE 754 `REAL` (64-bit). Financial calculations
require careful use of `ROUND()` at every step — which this project already enforces
consistently (e.g., `round(b.Close, 2)` in `11_wc_backtest_details.sql`). Moving more
math into SQL means more places where a missing `ROUND()` introduces silent cent-level
drift that accumulates over thousands of trades.

#### No Enum Safety, No Typed Constants
The `ExitReason` typed constant in Go — `ExitReasonStopLoss`, `ExitReasonTrailingStop` —
becomes a raw `TEXT` column in SQLite. A typo (`"STOP_LSOS"`) inserts silently. A `CHECK`
constraint helps but is not enforced by the language itself. Every consumer of that column
must remember the exact string values.

#### Testing Requires Database Fixtures
Every unit test for SQL logic needs a populated SQLite database. Test setup is heavier,
test isolation requires `DROP TABLE` / recreate sequences, and parallelism requires
separate database files. Go struct-based tests are far cheaper to write and run.

#### Schema Becomes the Interface Contract
When all models live in SQL, the schema IS the API. Renaming a column is a breaking change
that affects every downstream query, every Go scan call, and every external report. Without
a migration framework and disciplined versioning, schema churn creates silent breakage.

#### Loss of Go's Type System at the Boundary
When metrics are computed entirely in SQL, the Go layer receives untyped `interface{}` or
must manually assert types from `*sql.Rows`. The rich type information — `ExitReason`,
`time.Time` for `ParsedDt`, the distinction between `int64` volume and `float64` price —
evaporates at the query boundary and must be reconstructed in Go.

---

## Summary: The Right Boundary for This Codebase

```
┌─────────────────────────────────────────────────────────────────┐
│                        GO LAYER                                 │
│  • Domain types (Bar, Trade, Signal, Position, Account)         │
│  • Simulation loop (bar-by-bar, stateful)                       │
│  • Broker execution (order fill, position lifecycle)            │
│  • API / JSON / Web serving                                     │
│  • Test harness for business logic                              │
└─────────────────────────┬───────────────────────────────────────┘
                          │  SQL driver (go-sqlite3 / modernc)
┌─────────────────────────▼───────────────────────────────────────┐
│                      SQLITE LAYER                               │
│  • All rolling-window math (minlow, maxhigh, trailing stops)    │
│  • Signal detection pipeline (01_ → 13_)                        │
│  • Aggregation (summary, win-rate, avg gain)                    │
│  • Filtering and ranking (top symbols by 90th-pct threshold)    │
│  • Intermediate materialized tables (inspectable, reproducible) │
└─────────────────────────────────────────────────────────────────┘
```

**Go owns the verbs. SQLite owns the nouns and the math.**

The Go models in `pkg/backtest/models/models.go` are not duplicating the SQL schema — they are
the typed, safe, IDE-navigable representation of what SQLite computed. They are the handoff
point between the database's set-based world and Go's imperative, stateful world. Both
layers are necessary. Neither should fully absorb the other.
