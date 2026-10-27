package refdb

import (
	"fmt"
	"os"
	"sort"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// ExportModels writes a small model database at dstPath holding only the trained
// models of the signal symbols used by the strategy rows in bundlePath (the
// database Export wrote). family is "markov" or "tree"; srcPath is the full model
// database `train <family>` wrote. A program that cannot carry the whole file
// (trade_orchestrator on App Engine) ships this instead. A signal symbol with no
// trained model is an error, so a strategy cannot ship without its model. It
// returns the symbols exported.
func ExportModels(family, srcPath, bundlePath, dstPath string) ([]string, error) {
	var schemaStage, copyStage, strategyTable, metaTable string
	switch family {
	case "markov":
		schemaStage, copyStage, strategyTable, metaTable = "markov_model_schema", "models_export_markov", "markov_strategy", "markov_model_meta"
	case "tree":
		schemaStage, copyStage, strategyTable, metaTable = "tree_model_schema", "models_export_tree", "tree_strategy", "tree_model_meta"
	default:
		return nil, fmt.Errorf("export models: unknown family %q (markov or tree)", family)
	}
	if err := os.Remove(dstPath); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	db, err := storage.OpenSQLite(dstPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // the ATTACHes must be seen by the statements that follow
	for alias, path := range map[string]string{"src": srcPath, "ref": bundlePath} {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("export models: %w", err)
		}
		if _, err := db.Exec(`ATTACH DATABASE ? AS `+alias, path); err != nil {
			return nil, fmt.Errorf("export models: attach %s: %w", path, err)
		}
	}
	if err := storage.RunStage(db, schemaStage, nil); err != nil {
		return nil, err
	}
	if err := storage.RunStage(db, copyStage, nil); err != nil {
		return nil, err
	}
	var missing []string
	if err := db.Select(&missing, `SELECT DISTINCT UPPER(TRIM(signal_symbol)) FROM ref.`+strategyTable+
		` WHERE UPPER(TRIM(signal_symbol)) NOT IN (SELECT symbol FROM main.`+metaTable+`) ORDER BY 1`); err != nil {
		return nil, err
	}
	var got []string
	if err := db.Select(&got, `SELECT symbol FROM main.`+metaTable+` ORDER BY symbol`); err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return got, fmt.Errorf("export models: no trained %s model in %s for %v; run `train %s -symbols ...`", family, srcPath, missing, family)
	}
	return got, nil
}
