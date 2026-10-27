package trader

import (
	"encoding/json"
)

type Balances struct {
	CashBalance             float64 `json:"cashBalance"`
	CashAvailableForTrading float64 `json:"cashAvailableForTrading"`
	LiquidationValue        float64 `json:"liquidationValue"`
	BuyingPower             float64 `json:"buyingPower"`
}

type PositionInstrument struct {
	Symbol    string `json:"symbol"`
	AssetType string `json:"assetType"`
}

type Position struct {
	ShortQuantity                  float64            `json:"shortQuantity"`
	AveragePrice                   float64            `json:"averagePrice"`
	CurrentDayProfitLoss           float64            `json:"currentDayProfitLoss"`
	CurrentDayProfitLossPercentage float64            `json:"currentDayProfitLossPercentage"`
	LongQuantity                   float64            `json:"longQuantity"`
	MarketValue                    float64            `json:"marketValue"`
	Instrument                     PositionInstrument `json:"instrument"`
}

type SecuritiesAccount struct {
	Type              string     `json:"type"`
	AccountNumber     string     `json:"accountNumber"`
	Nickname          string     `json:"nickname"`
	CurrentBalances   Balances   `json:"currentBalances"`
	ProjectedBalances Balances   `json:"projectedBalances"`
	Positions         []Position `json:"positions"`
}

type ParsedAccount struct {
	SecuritiesAccount SecuritiesAccount `json:"securitiesAccount"`
}

type ResolvedAccount struct {
	AccountNumber string
	HashValue     string
	Details       SecuritiesAccount
}

// ParseSecuritiesAccount parses raw Schwab Account JSON into structured SecuritiesAccount.
func ParseSecuritiesAccount(raw json.RawMessage) (*SecuritiesAccount, error) {
	var sec SecuritiesAccount
	if err := json.Unmarshal(raw, &sec); err == nil && sec.AccountNumber != "" {
		return &sec, nil
	}

	var parsed ParsedAccount
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.SecuritiesAccount.AccountNumber != "" {
		return &parsed.SecuritiesAccount, nil
	}

	return &sec, nil
}

// GetBuyingPower returns available settled cash for a Roth IRA or cash account.
func GetBuyingPower(sec *SecuritiesAccount) float64 {
	if sec.CurrentBalances.CashAvailableForTrading > 0 {
		return sec.CurrentBalances.CashAvailableForTrading
	}
	if sec.ProjectedBalances.CashAvailableForTrading > 0 {
		return sec.ProjectedBalances.CashAvailableForTrading
	}
	if sec.CurrentBalances.CashBalance > 0 {
		return sec.CurrentBalances.CashBalance
	}
	return sec.CurrentBalances.BuyingPower
}
