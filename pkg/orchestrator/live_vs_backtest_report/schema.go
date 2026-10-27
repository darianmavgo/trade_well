package live_vs_backtest_report

import (
	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// JobName is the job_runs.job value and the CLI subcommand.
const JobName = "live_vs_backtest_report"

// reportTables are the tables of one report build, kept in Turso. Each run has its own copy
// (see runtables), so a report can be audited after it was built. Only what is specific to the
// report lives here: the decisions and slippage it shows are read from live_vs_backtest_trade and
// live_vs_backtest_slippage, and nothing is copied from the account mirror or the market file.
var reportTables = []string{"live_vs_backtest_report_meta", "live_vs_backtest_report_note", "live_vs_backtest_report_round_trip"}

// keepRuns is how many builds' tables are kept in Turso.
const keepRuns = 3

var reportDDL = orchestratorsql.Q("live_vs_backtest_report/report_ddl")
