// Package sqliteguard holds the test that keeps SQLite access in trade_orchestrator easy to see.
//
// The operational database is Turso, and the only reasons this repo may open a SQLite file are
// the two kinds below. Every function that opens one is named for its kind, and lives in a file
// named sqlite_<kind>[_<what>].go, so a reader (or rg 'sqlite_') finds every place at once. The
// test in this package fails when a function opens SQLite outside that scheme.
//
//	Asset        a file built in backtestgosqlite and read here: a strategy definition
//	             (strategies.db), a trained model (hmm_models.db, markov_models.db,
//	             tree_models.db) or a backtest result database used to audit a strategy.
//	             Function names contain "Asset"; files are sqlite_strategy_asset_*.go,
//	             sqlite_model_asset_*.go and sqlite_backtest_result_asset_*.go.
//	WorkingCopy  a local copy of data that lives in Turso, built for backtestgosqlite's SQL
//	             stages to read (the market file, market_history_daily.db). Rebuilt from Turso; never the
//	             record. Function names contain "WorkingCopy"; files are sqlite_working_copy_*.go.
//
// There is no Scratch kind. A calculation that used to run in a throwaway SQLite database now
// runs in Turso, in tables named for the run (package runtables), so nothing is local. The win-rate
// comparison drops its tables when it returns; the report and sync calculations keep their newest
// few for audit. A file named sqlite_scratch_*.go, or a function that opens an in-memory database,
// fails the test.
package sqliteguard
