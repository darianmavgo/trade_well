// Package layers holds the import-direction test for the single trade_well module.
//
// Before the three repos were merged, the module boundary kept the layers apart: backtestgosqlite
// could not import schwaber because schwaber was not in its go.mod. In one module nothing stops
// that, so TestImportsPointDown does. The layers are (CLAUDE.md "Repo hierarchy"):
//
//	pkg/schwaber       Foundation: Schwab client and shared infrastructure. Imports no other layer.
//	pkg/backtest       Calculation: strategies, signals, backtests, SQL pipelines. Imports no other layer.
//	sql                Embedded SQL for pkg/backtest. Imports nothing in the module.
//	pkg/orchestrator   Orchestration: schedules and sequences. May import schwaber and backtest.
//	cmd                Thin command wrappers. May import anything.
package layers
