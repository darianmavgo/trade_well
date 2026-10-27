package datasource

import (
	"context"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// FetchRequest defines the parameters for querying historical market data.
type FetchRequest struct {
	Symbol     string    `json:"symbol"`
	StartDate  time.Time `json:"start_date"`
	EndDate    time.Time `json:"end_date"`
	Timeframe  string    `json:"timeframe,omitempty"`   // "1d", "1h", "5m" (defaults to "1d")
	AssetClass string    `json:"asset_class,omitempty"` // "equity", "crypto", etc.
}

// DataSource is the uniform interface for fetching OHLCV bars from any provider or storage backend.
type DataSource interface {
	// Name returns the provider name (e.g. "yahoo", "alpaca", "polygon", "sqlite").
	Name() string

	// Fetch loads historical bars matching the request parameters.
	Fetch(ctx context.Context, req FetchRequest) ([]models.Bar, error)
}
