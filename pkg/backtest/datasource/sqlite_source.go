package datasource

import (
	"context"
	"fmt"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

// SQLiteDataSource reads historical price bars from an existing SQLite database table.
type SQLiteDataSource struct {
	DB        *sqlx.DB
	TableName string
}

// NewSQLiteDataSource creates a SQLite data source reading from the specified table.
func NewSQLiteDataSource(db *sqlx.DB, tableName string) *SQLiteDataSource {
	if tableName == "" {
		tableName = "backtest_start"
	}
	return &SQLiteDataSource{
		DB:        db,
		TableName: tableName,
	}
}

func (s *SQLiteDataSource) Name() string {
	return "sqlite"
}

func (s *SQLiteDataSource) Fetch(ctx context.Context, req FetchRequest) ([]models.Bar, error) {
	if s.DB == nil {
		return nil, fmt.Errorf("sqlite database connection is nil")
	}

	startStr := ""
	if !req.StartDate.IsZero() {
		startStr = req.StartDate.Format("2006-01-02")
	}
	endStr := ""
	if !req.EndDate.IsZero() {
		endStr = req.EndDate.Format("2006-01-02")
	}

	var symbols []string
	if req.Symbol != "" {
		symbols = []string{req.Symbol}
	}

	barsBySymbol, _, err := storage.FetchBars(s.DB, s.TableName, symbols, startStr, endStr)
	if err != nil {
		return nil, err
	}

	if req.Symbol != "" {
		return barsBySymbol[req.Symbol], nil
	}

	var allBars []models.Bar
	for _, bars := range barsBySymbol {
		allBars = append(allBars, bars...)
	}
	return allBars, nil
}
