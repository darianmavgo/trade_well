// Package appenv resolves configuration from the process environment and the
// project .env file, so every command agrees on the API key and where the
// reports, reference data and market data live.
//
//	POLYGON_API_KEY  Polygon.io key
//	APP_FOLDER       project root (default: current directory)
//
// Real environment variables win over .env values. .env is searched in the
// current directory, its two parents, and beside/above the running binary (bin/<cmd> finds the
// project root's .env).
//
// The .env file is a local convenience only. Nothing deployed may depend on it: App Engine gets
// its configuration from gs://<bucket>/config/env, the upload rules (.gcloudignore) never ship a
// .env, and the orchestrator calls IgnoreDotEnv first thing on App Engine so a stray file on the
// instance cannot change behaviour.
package appenv

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	once      sync.Once
	fileKV    map[string]string
	dotEnvOff atomic.Bool
)

// IgnoreDotEnv makes Get answer from the process environment only, from now on, even for values
// already read from a .env file. Call it before anything reads configuration when the process
// must not depend on a local file (App Engine).
func IgnoreDotEnv() { dotEnvOff.Store(true) }

func load() {
	fileKV = map[string]string{}
	dirs := []string{".", "..", "../.."}
	if exe, err := os.Executable(); err == nil { // bin/<cmd> -> project root
		d := filepath.Dir(exe)
		dirs = append(dirs, d, filepath.Dir(d))
	}
	for _, dir := range dirs {
		f, err := os.Open(filepath.Join(dir, ".env"))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if _, seen := fileKV[k]; !seen { // nearest .env wins
				fileKV[k] = v
			}
		}
		f.Close()
	}
}

// Get returns the value for key from the environment, then .env ("" if unset).
func Get(key string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	if dotEnvOff.Load() {
		return ""
	}
	once.Do(load)
	return strings.TrimSpace(fileKV[key])
}

func sub(key, def string) string {
	if v := Get(key); v != "" {
		return filepath.Clean(v)
	}
	return def
}

// Folder is the project root.
func Folder() string { return sub("APP_FOLDER", ".") }

// Reports is the reports directory.
func Reports() string { return filepath.Join(Data(), "reports") }

// Ref is the reference-data directory.
func Ref() string { return filepath.Join(Folder(), "refdata") }

// Data is the market-data directory.
func Data() string { return filepath.Join(Folder(), "data") }

// RefDB is the reference database (streak, tree, markov, and hold strategy tables).
func RefDB() string { return filepath.Join(Ref(), "strategies.db") }

// UniverseDB is the universe database (all stocks, etfs, categorization, leverage).
func UniverseDB() string { return filepath.Join(Ref(), "universe.db") }

// MarketDB is the market history database. It is the hourly database: hourly
// bars (rows with a "YYYY-MM-DD HH:MM:SS" Date, timeframe 1h) are the only bars
// that are downloaded, and the session rows ("YYYY-MM-DD" Date, timeframe 1d)
// that signals and the simulation clock read are derived from them in the same
// table (see pkg/sessions). There is no separate daily database.
func MarketDB() string { return HourlyDB() }

// HourlyDB is the hourly bars and the sessions derived from them.
func HourlyDB() string { return filepath.Join(Data(), "market_history_hourly.db") }

// DailyBarsDB is long-history daily closes for the few symbols a model needs more years of
// than the hourly database holds (written by daily_bars; the hourly database is never touched).
// It is not a market database: signals and the simulator still read only MarketDB.
func DailyBarsDB() string { return filepath.Join(Data(), "daily_bars.db") }

// MinuteDB is the minute bars (1m, 5m, ...).
func MinuteDB() string { return filepath.Join(Data(), "market_history_minute.db") }

// BarDB returns the database that holds bars of the timeframe, given the market
// database path. Hourly bars and their derived sessions share one file, so an
// hourly path (a name ending in "_hourly") and every daily or hourly timeframe
// map to it; any other path gets the hourly or minute database beside it
// ("market_history.db" -> "market_history_hourly.db"), so a temp database gets
// temp siblings.
func BarDB(marketDB, timeframe string) string {
	suffix := "_hourly"
	switch tf := strings.ToLower(strings.TrimSpace(timeframe)); {
	case strings.HasSuffix(tf, "m") && tf != "":
		suffix = "_minute"
	case tf == "" || tf == "1d" || tf == "d":
		return marketDB
	}
	ext := filepath.Ext(marketDB)
	base := strings.TrimSuffix(marketDB, ext)
	if strings.HasSuffix(base, suffix) {
		return marketDB
	}
	return base + suffix + ext
}

// RequireHourly rejects a market database path that is not the hourly one, so a
// leftover daily file is never read by mistake.
func RequireHourly(path string) error {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.HasSuffix(base, "_hourly") {
		return nil
	}
	return fmt.Errorf("%s is not an hourly database: daily databases are retired, pass %s", path, HourlyDB())
}

// MarkovDB is the trained Markov models (written by train markov, read by backtest).
func MarkovDB() string { return filepath.Join(Data(), "markov_models.db") }

// HMMDB is the trained hidden Markov models and their daily regime predictions (written by
// train hmm, extended by the hmm strategies' Prepare, never refit by either).
func HMMDB() string { return filepath.Join(Data(), "hmm_models.db") }

// TreeDB is the trained decision trees (written by train tree, read by backtest).
func TreeDB() string { return filepath.Join(Data(), "tree_models.db") }

// DataFile returns a path to name inside the data directory.
func DataFile(name string) string { return filepath.Join(Data(), name) }

// ReportFile returns a path inside the reports directory. Absolute paths pass
// through; a leading "data/reports/" or "reports/" is treated as the reports
// directory itself, so it is never doubled (reports/data/reports/...).
func ReportFile(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	sep := string(filepath.Separator)
	p = filepath.Clean(p)
	p = strings.TrimPrefix(p, "data"+sep)
	p = strings.TrimPrefix(p, "reports"+sep)
	return filepath.Join(Reports(), p)
}
