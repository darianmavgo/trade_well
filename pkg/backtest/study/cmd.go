package study

import (
	"flag"
	"fmt"
	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"log"
	"path/filepath"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"
)

// Config holds the settings of a study run.
type Config struct {
	DB     string // source market DB
	Study  string // study ID
	OutDir string // directory for the results DB
	Symbol string // optional symbol to run the study on
}

// DefaultConfig returns the CLI defaults.
func DefaultConfig() Config {
	return Config{DB: cliutils.GetDefaultMarketDB(), OutDir: appenv.Reports()}
}

// Run executes one registered study and returns the path of its results DB.
func Run(cfg Config) (string, error) {
	if cfg.Study == "" {
		return "", fmt.Errorf("no study specified. Run with -list to see available studies or provide -study <id>")
	}
	s, exists := Get(strings.TrimSpace(cfg.Study))
	if !exists {
		return "", fmt.Errorf("study '%s' not found", cfg.Study)
	}
	resultsDBPath := filepath.Join(cfg.OutDir, fmt.Sprintf("%s.db", s.ID()))
	s.SetDatabases(cfg.DB, resultsDBPath)

	if sa, ok := s.(SymbolAware); ok {
		sa.SetSymbol(cfg.Symbol)
	}

	if err := s.Run(); err != nil {
		return "", fmt.Errorf("study execution failed: %w", err)
	}
	return resultsDBPath, nil
}

// Main is the CLI entry point.
func Main() {
	cfg := DefaultConfig()
	flag.StringVar(&cfg.DB, "db", cfg.DB, "Path to source SQLite DB containing historical market bars")
	flag.StringVar(&cfg.Study, "study", "", "Study ID to run (e.g. march_april_voo_gld_uten)")
	flag.StringVar(&cfg.OutDir, "out-dir", cfg.OutDir, "Directory to write study results (SQLite database)")
	flag.StringVar(&cfg.Symbol, "symbol", "", "Optional target symbol for studies that support it (e.g. QQQ)")
	listFlag := flag.Bool("list", false, "List all registered studies")
	flag.Parse()

	if *listFlag {
		fmt.Println("Available Studies:")
		studies := List()
		for _, s := range studies {
			fmt.Printf("  - %s: %s\n    %s\n", s.ID(), s.Name(), s.Description())
		}
		return
	}

	resultsDBPath, err := Run(cfg)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n✨ Study successfully saved to %s\n", resultsDBPath)
}
