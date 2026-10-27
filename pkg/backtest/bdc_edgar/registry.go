// Package bdc_edgar ingests SEC EDGAR 10-Q and 10-K Schedules of Investments
// for publicly traded and non-traded Business Development Companies (BDCs)
// managed by Blackstone, Blue Owl, Ares, and Apollo, and runs the SQL slice-table
// pipeline (sql/stages/bdc_calc and sql/stages/bdc_publish) to track GPU/AI
// infrastructure SPVs, data center credit facilities, spreads, fair-value
// markdowns, and non-accrual transitions over time.
package bdc_edgar

import (
	"fmt"
	"strings"
)

// BDCInfo identifies a Business Development Company filer on SEC EDGAR.
type BDCInfo struct {
	Ticker  string `json:"ticker"`
	Name    string `json:"name"`
	Manager string `json:"manager"`
	CIK     string `json:"cik"`    // 10-digit zero-padded CIK
	Traded  bool   `json:"traded"` // true if publicly listed, false if non-traded public filer
}

// DefaultBDCs lists the target publicly traded and non-traded BDCs managed by
// Blackstone, Blue Owl, Ares, and Apollo that participate in GPU, data center,
// and technology credit facilities.
var DefaultBDCs = []BDCInfo{
	{
		Ticker:  "BXSL",
		Name:    "Blackstone Secured Lending Fund",
		Manager: "Blackstone",
		CIK:     "0001736035",
		Traded:  true,
	},
	{
		Ticker:  "BCRED",
		Name:    "Blackstone Private Credit Fund",
		Manager: "Blackstone",
		CIK:     "0001803498",
		Traded:  false,
	},
	{
		Ticker:  "OBDC",
		Name:    "Blue Owl Capital Corp",
		Manager: "Blue Owl",
		CIK:     "0001655888",
		Traded:  true,
	},
	{
		Ticker:  "OTF",
		Name:    "Blue Owl Technology Finance Corp",
		Manager: "Blue Owl",
		CIK:     "0001747777",
		Traded:  true,
	},
	{
		Ticker:  "OTIC",
		Name:    "Blue Owl Technology Income Corp",
		Manager: "Blue Owl",
		CIK:     "0001869453",
		Traded:  false,
	},
	{
		Ticker:  "OCIC",
		Name:    "Blue Owl Credit Income Corp",
		Manager: "Blue Owl",
		CIK:     "0001812554",
		Traded:  false,
	},
	{
		Ticker:  "ARCC",
		Name:    "Ares Capital Corp",
		Manager: "Ares",
		CIK:     "0001287750",
		Traded:  true,
	},
	{
		Ticker:  "ASIF",
		Name:    "Ares Strategic Income Fund",
		Manager: "Ares",
		CIK:     "0001930510",
		Traded:  false,
	},
	{
		Ticker:  "MFIC",
		Name:    "MidCap Financial Investment Corp",
		Manager: "Apollo",
		CIK:     "0001278752",
		Traded:  true,
	},
	{
		Ticker:  "ADS",
		Name:    "Apollo Debt Solutions BDC",
		Manager: "Apollo",
		CIK:     "0001837532",
		Traded:  false,
	},
}

// NormalizeCIK formats a numeric CIK string as a 10-digit zero-padded string.
func NormalizeCIK(cik string) string {
	s := strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(cik), "CIK"))
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0000000000"
	}
	if len(s) >= 10 {
		return s
	}
	return strings.Repeat("0", 10-len(s)) + s
}

// CIKIntString strips leading zeros from a CIK for EDGAR Archives URL paths.
func CIKIntString(cik string) string {
	s := strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(cik), "CIK"))
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0"
	}
	return s
}

// ResolveBDCs resolves a list of ticker symbols, manager names, or CIKs against
// DefaultBDCs. An empty input slice returns all DefaultBDCs.
func ResolveBDCs(tokens []string) ([]BDCInfo, error) {
	if len(tokens) == 0 {
		out := make([]BDCInfo, len(DefaultBDCs))
		copy(out, DefaultBDCs)
		return out, nil
	}
	seen := map[string]bool{}
	var out []BDCInfo
	for _, raw := range tokens {
		for _, tok := range strings.Split(raw, ",") {
			q := strings.TrimSpace(tok)
			if q == "" {
				continue
			}
			qu := strings.ToUpper(q)
			matched := false
			for _, b := range DefaultBDCs {
				if strings.EqualFold(b.Ticker, qu) ||
					strings.EqualFold(b.Manager, qu) ||
					NormalizeCIK(b.CIK) == NormalizeCIK(qu) {
					if !seen[b.Ticker] {
						seen[b.Ticker] = true
						out = append(out, b)
					}
					matched = true
				}
			}
			if !matched {
				// Allow arbitrary numeric CIK if digits only
				digitsOnly := true
				trimmed := strings.TrimPrefix(qu, "CIK")
				for _, r := range trimmed {
					if r < '0' || r > '9' {
						digitsOnly = false
						break
					}
				}
				if digitsOnly && len(trimmed) > 0 {
					cik := NormalizeCIK(trimmed)
					if !seen[cik] {
						seen[cik] = true
						out = append(out, BDCInfo{
							Ticker:  "CIK" + CIKIntString(cik),
							Name:    "Custom BDC " + cik,
							Manager: "Custom",
							CIK:     cik,
							Traded:  false,
						})
					}
					matched = true
				}
			}
			if !matched {
				return nil, fmt.Errorf("unknown BDC ticker, manager, or CIK %q", q)
			}
		}
	}
	return out, nil
}
