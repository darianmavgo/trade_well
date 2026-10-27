package study

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

type VooGldUtenStudy struct {
	id            string
	name          string
	description   string
	marketDBPath  string
	resultsDBPath string
}

func init() {
	Register(&VooGldUtenStudy{
		id:          "march_april_voo_gld_uten",
		name:        "VOO, GLD, UTEN 1m Analysis (Mar/Apr 2025)",
		description: "Pivots 1-minute data for VOO, GLD, UTEN into a prototyping table and computes Granger Causality and Volatility Spillover in pure Go.",
	})
}

func (s *VooGldUtenStudy) ID() string {
	return s.id
}

func (s *VooGldUtenStudy) Name() string {
	return s.name
}

func (s *VooGldUtenStudy) Description() string {
	return s.description
}

func (s *VooGldUtenStudy) SetDatabases(marketDBPath, resultsDBPath string) {
	s.marketDBPath = marketDBPath
	s.resultsDBPath = resultsDBPath
}

func (s *VooGldUtenStudy) Run() error {
	log.Printf("Running study: %s", s.name)

	if err := os.MkdirAll(filepath.Dir(s.resultsDBPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	db, err := sqlx.Open("sqlite", s.resultsDBPath)
	if err != nil {
		return fmt.Errorf("failed to open results db: %w", err)
	}
	defer db.Close()

	attachQuery := fmt.Sprintf("ATTACH DATABASE '%s' AS market;", s.marketDBPath)
	if _, err := db.Exec(attachQuery); err != nil {
		return fmt.Errorf("failed to attach market database: %w", err)
	}

	log.Println("Pivoting data into prototyping sandbox table...")

	// Create prototyping table by aligning minute data for VOO, GLD, UTEN
	// We assume we want data strictly in March and April 2025
	pivotSQL := `
		DROP TABLE IF EXISTS prototyping;
		CREATE TABLE prototyping AS
		WITH 
			voo AS (
				SELECT date, close, ((close - open) / open) as return 
				FROM market.backtest_start 
				WHERE symbol = 'VOO' AND timeframe = '1m' AND date >= '2025-03-01' AND date < '2025-05-01'
			),
			gld AS (
				SELECT date, close, ((close - open) / open) as return 
				FROM market.backtest_start 
				WHERE symbol = 'GLD' AND timeframe = '1m' AND date >= '2025-03-01' AND date < '2025-05-01'
			),
			uten AS (
				SELECT date, close, ((close - open) / open) as return 
				FROM market.backtest_start 
				WHERE symbol = 'UTEN' AND timeframe = '1m' AND date >= '2025-03-01' AND date < '2025-05-01'
			)
		SELECT 
			voo.date as date,
			voo.close as voo_close,
			voo.return as voo_return,
			gld.close as gld_close,
			gld.return as gld_return,
			uten.close as uten_close,
			uten.return as uten_return
		FROM voo
		INNER JOIN gld ON voo.date = gld.date
		INNER JOIN uten ON voo.date = uten.date
		ORDER BY voo.date ASC;
	`
	if _, err := db.Exec(pivotSQL); err != nil {
		return fmt.Errorf("failed to create prototyping table: %w", err)
	}

	var count int
	_ = db.Get(&count, "SELECT COUNT(*) FROM prototyping;")
	log.Printf("Successfully created prototyping sandbox with %d aligned 1-minute rows.", count)

	type PrototypeRow struct {
		Date       string  `db:"date"`
		VooClose   float64 `db:"voo_close"`
		VooReturn  float64 `db:"voo_return"`
		GldClose   float64 `db:"gld_close"`
		GldReturn  float64 `db:"gld_return"`
		UtenClose  float64 `db:"uten_close"`
		UtenReturn float64 `db:"uten_return"`
	}

	var rows []PrototypeRow
	if err := db.Select(&rows, "SELECT date, voo_close, voo_return, gld_close, gld_return, uten_close, uten_return FROM prototyping ORDER BY date ASC"); err != nil {
		return fmt.Errorf("failed to query prototyping: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("prototyping table is empty")
	}

	log.Printf("Loaded %d rows. Computing Granger Causality in pure Go...", len(rows))

	dates := make([]string, len(rows))
	vooRet := make([]float64, len(rows))
	gldRet := make([]float64, len(rows))
	utenRet := make([]float64, len(rows))
	for i, r := range rows {
		dates[i] = r.Date
		vooRet[i] = r.VooReturn
		gldRet[i] = r.GldReturn
		utenRet[i] = r.UtenReturn
	}

	// 1. Granger Causality: VOO -> GLD and VOO -> UTEN
	maxLag := 10
	var allGranger []GrangerResult

	gcGLD, err := ComputeGrangerCausality("VOO", "GLD", vooRet, gldRet, maxLag)
	if err != nil {
		log.Printf("Error computing VOO->GLD granger: %v", err)
	} else {
		allGranger = append(allGranger, gcGLD...)
	}

	gcUTEN, err := ComputeGrangerCausality("VOO", "UTEN", vooRet, utenRet, maxLag)
	if err != nil {
		log.Printf("Error computing VOO->UTEN granger: %v", err)
	} else {
		allGranger = append(allGranger, gcUTEN...)
	}

	// Create granger_causality table
	if _, err := db.Exec(`
		DROP TABLE IF EXISTS granger_causality;
		CREATE TABLE granger_causality (
			predictor TEXT,
			target TEXT,
			lag_minutes INTEGER,
			p_value REAL
		);
	`); err != nil {
		return fmt.Errorf("failed to create granger_causality table: %w", err)
	}

	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	stmt, err := tx.Preparex("INSERT INTO granger_causality (predictor, target, lag_minutes, p_value) VALUES (?, ?, ?, ?)")
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to prepare granger insert: %w", err)
	}
	for _, gr := range allGranger {
		if _, err := stmt.Exec(gr.Predictor, gr.Target, gr.LagMinutes, gr.PValue); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to insert granger row: %w", err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit granger_causality: %w", err)
	}
	log.Printf("Wrote granger_causality table (%d rows).", len(allGranger))

	// 2. Volatility Spillover (15m rolling variance)
	log.Println("Computing Volatility Spillover (15m rolling variance)...")
	window := 15
	vooVars := ComputeRollingVariance(vooRet, window)
	gldVars := ComputeRollingVariance(gldRet, window)
	utenVars := ComputeRollingVariance(utenRet, window)

	outLen := len(vooVars)
	offset := window - 1 // first window - 1 rows dropped due to rolling window

	if _, err := db.Exec(`
		DROP TABLE IF EXISTS volatility_spillover;
		CREATE TABLE volatility_spillover (
			date TEXT,
			voo_var_15m REAL,
			gld_var_15m REAL,
			uten_var_15m REAL
		);
	`); err != nil {
		return fmt.Errorf("failed to create volatility_spillover table: %w", err)
	}

	txVol, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to start transaction for volatility: %w", err)
	}
	volStmt, err := txVol.Preparex("INSERT INTO volatility_spillover (date, voo_var_15m, gld_var_15m, uten_var_15m) VALUES (?, ?, ?, ?)")
	if err != nil {
		_ = txVol.Rollback()
		return fmt.Errorf("failed to prepare volatility insert: %w", err)
	}
	for i := 0; i < outLen; i++ {
		d := dates[offset+i]
		if _, err := volStmt.Exec(d, vooVars[i], gldVars[i], utenVars[i]); err != nil {
			_ = txVol.Rollback()
			return fmt.Errorf("failed to insert volatility row: %w", err)
		}
	}
	volStmt.Close()
	if err := txVol.Commit(); err != nil {
		return fmt.Errorf("failed to commit volatility_spillover: %w", err)
	}

	// 3. Print Correlation Matrix
	rVooGld := ComputeCorrelation(vooVars, gldVars)
	rVooUten := ComputeCorrelation(vooVars, utenVars)
	rGldUten := ComputeCorrelation(gldVars, utenVars)

	fmt.Println("\nVolatility Correlation Matrix (15m):")
	fmt.Printf("%15s %15s %15s %15s\n", "", "voo_var_15m", "gld_var_15m", "uten_var_15m")
	fmt.Printf("%15s %15.6f %15.6f %15.6f\n", "voo_var_15m", 1.0, rVooGld, rVooUten)
	fmt.Printf("%15s %15.6f %15.6f %15.6f\n", "gld_var_15m", rVooGld, 1.0, rGldUten)
	fmt.Printf("%15s %15.6f %15.6f %15.6f\n", "uten_var_15m", rVooUten, rGldUten, 1.0)

	log.Println("Study execution complete. Results saved to:", s.resultsDBPath)
	return nil
}
