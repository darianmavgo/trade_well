// Package hoursim runs backtestgosqlite's signals through backtestgo's hourly simulator
// (backtestgo/pkg/sim), the engine that judges every fill and exit on the hour it can happen.
// It converts the daily-simulator types (models.Signal, strategy.StrategyConfig, models.Trade)
// to sim's and back, so callers keep their report, trade and equity-curve types.
//
// Where a symbol has no hourly bars for a session, that session's daily bar is played as a
// single bar. Nothing can then exit on the bar it was entered on, which is the conservative
// reading of a daily bar, and the symbols and sessions that used it are reported in Output.
package hoursim
