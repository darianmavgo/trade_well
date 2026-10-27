// Package sqlfiles embeds the strategy SQL pipelines (sql/strategies/**) into
// the binary. Binaries that import backtestgosqlite as a library (e.g.
// trade_orchestrator on App Engine) have no repo checkout on disk, so a pipeline
// directory resolved against the working directory does not exist there.
package sqlfiles

import "embed"

// Strategies holds every file under sql/strategies, addressed as
// "strategies/<pipeline>/<file>.sql".
//
//go:embed strategies
var Strategies embed.FS

// Validation holds the walk-forward and curve-fit summary scripts,
// addressed as "validation/<file>.sql".
//
//go:embed validation
var Validation embed.FS

// Stages holds shared calculation stages written into a run's calc database
// (for example bar_sma), addressed as "stages/<stage>/<file>.sql".
//
//go:embed stages
var Stages embed.FS

// SymbolStatsView is the v_symbol_stats view over backtest_start, created in
// every market database by storage.EnsureBarTable.
//
//go:embed view_symbol_stats.sql
var SymbolStatsView string

// Views holds the calculations written as views over backtest_start,
// addressed as "views/<file>.sql" and run in name order by pkg/export_views.
//
//go:embed views
var Views embed.FS
