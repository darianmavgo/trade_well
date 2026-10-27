package trader

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
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

// ResolveRothAccount finds the Roth IRA account given linked account list and configured target.
func ResolveRothAccount(
	linkedAccounts []schwab.LinkedAccount,
	fullAccounts []schwab.Account,
	configuredTarget string,
) (*ResolvedAccount, error) {
	if len(linkedAccounts) == 0 {
		return nil, fmt.Errorf("no linked Schwab accounts found")
	}

	// Map account number -> HashValue
	hashMap := make(map[string]string)
	for _, la := range linkedAccounts {
		hashMap[la.AccountNumber] = la.HashValue
	}

	// Map account number -> SecuritiesAccount details
	detailsMap := make(map[string]SecuritiesAccount)
	for _, fa := range fullAccounts {
		sec, err := ParseSecuritiesAccount(fa.SecuritiesAccount)
		if err == nil && sec.AccountNumber != "" {
			detailsMap[sec.AccountNumber] = *sec
		}
	}

	// 1. Explicitly configured target account number
	if configuredTarget != "" {
		targetClean := strings.TrimSpace(configuredTarget)
		hash, hasHash := hashMap[targetClean]
		details, hasDetails := detailsMap[targetClean]
		if hasHash && hasDetails {
			return &ResolvedAccount{
				AccountNumber: targetClean,
				HashValue:     hash,
				Details:       details,
			}, nil
		}
		return nil, fmt.Errorf("configured account number '%s' not found in your Schwab accounts", targetClean)
	}

	// 2. Search for ROTH / IRA keywords in Type or Nickname
	var candidates []ResolvedAccount
	for _, la := range linkedAccounts {
		details, ok := detailsMap[la.AccountNumber]
		if !ok {
			continue
		}
		typeUpper := strings.ToUpper(details.Type)
		nickUpper := strings.ToUpper(details.Nickname)

		if strings.Contains(typeUpper, "ROTH") || strings.Contains(nickUpper, "ROTH") || strings.Contains(typeUpper, "IRA") {
			candidates = append(candidates, ResolvedAccount{
				AccountNumber: la.AccountNumber,
				HashValue:     la.HashValue,
				Details:       details,
			})
		}
	}

	if len(candidates) == 1 {
		return &candidates[0], nil
	}
	if len(candidates) > 1 {
		var nums []string
		for _, c := range candidates {
			nums = append(nums, c.AccountNumber)
		}
		return nil, fmt.Errorf("multiple IRA accounts detected (%s). Set SCHWAB_ROTH_ACCOUNT_NUMBER in .env", strings.Join(nums, ", "))
	}

	// 3. If user only has 1 account, return it
	if len(linkedAccounts) == 1 {
		accNum := linkedAccounts[0].AccountNumber
		return &ResolvedAccount{
			AccountNumber: accNum,
			HashValue:     linkedAccounts[0].HashValue,
			Details:       detailsMap[accNum],
		}, nil
	}

	return nil, fmt.Errorf("could not automatically identify your Roth IRA. Please run `schwaber accounts` and set SCHWAB_ROTH_ACCOUNT_NUMBER in .env")
}
