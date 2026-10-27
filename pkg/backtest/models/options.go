package models

// OptionContract is one listed option (e.g. O:VOO260821C00640000).
type OptionContract struct {
	Ticker            string  `db:"ticker" json:"ticker"`
	Underlying        string  `db:"underlying" json:"underlying"`
	Expiry            string  `db:"expiry" json:"expiry"` // YYYY-MM-DD
	Strike            float64 `db:"strike" json:"strike"`
	Right             string  `db:"contract_type" json:"right"` // "call" or "put"
	SharesPerContract int     `db:"shares_per_contract" json:"shares_per_contract"`
}

// OptionBar is one end-of-day aggregate for an option contract. Polygon only
// emits a bar on days the contract actually traded, so thinly traded strikes
// have gaps and Close is a last-trade price, not a bid/ask mid.
type OptionBar struct {
	Ticker string  `db:"ticker" json:"ticker"`
	Date   string  `db:"Date" json:"date"`
	Open   float64 `db:"open" json:"open"`
	High   float64 `db:"high" json:"high"`
	Low    float64 `db:"low" json:"low"`
	Close  float64 `db:"close" json:"close"`
	Volume int64   `db:"volume" json:"volume"`
	Trades int64   `db:"trades" json:"trades"`
}
