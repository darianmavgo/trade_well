package bdc_edgar

import "io"

// FilingMeta holds metadata for one SEC EDGAR 10-Q or 10-K filing.
type FilingMeta struct {
	AccessionNumber string `db:"accession_number" json:"accession_number"`
	BDCTicker       string `db:"bdc_ticker" json:"bdc_ticker"`
	BDCName         string `db:"bdc_name" json:"bdc_name"`
	Manager         string `db:"manager" json:"manager"`
	CIK             string `db:"cik" json:"cik"`
	Form            string `db:"form" json:"form"`
	FilingDate      string `db:"filing_date" json:"filing_date"`
	ReportDate      string `db:"report_date" json:"report_date"`
	PrimaryDocument string `db:"primary_document" json:"primary_document"`
	SourceURL       string `db:"source_url" json:"source_url"`
	RowCount        int    `db:"row_count" json:"row_count"`
}

// RawSOIRow represents one extracted Schedule of Investments line item before
// SQL classification and time-series calculation.
type RawSOIRow struct {
	AccessionNumber string  `db:"accession_number" json:"accession_number"`
	BDCTicker       string  `db:"bdc_ticker" json:"bdc_ticker"`
	CIK             string  `db:"cik" json:"cik"`
	Form            string  `db:"form" json:"form"`
	FilingDate      string  `db:"filing_date" json:"filing_date"`
	ReportDate      string  `db:"report_date" json:"report_date"`
	PeriodDate      string  `db:"period_date" json:"period_date"`
	RowNum          int     `db:"row_num" json:"row_num"`
	RawContextID    string  `db:"raw_context_id" json:"raw_context_id"`
	BorrowerName    string  `db:"borrower_name" json:"borrower_name"`
	Industry        string  `db:"industry" json:"industry"`
	InvestmentType  string  `db:"investment_type" json:"investment_type"`
	ReferenceRate   string  `db:"reference_rate" json:"reference_rate"`
	SpreadBps       float64 `db:"spread_bps" json:"spread_bps"`
	InterestRatePct float64 `db:"interest_rate_pct" json:"interest_rate_pct"`
	PIKRatePct      float64 `db:"pik_rate_pct" json:"pik_rate_pct"`
	FloorRatePct    float64 `db:"floor_rate_pct" json:"floor_rate_pct"`
	RateText        string  `db:"rate_text" json:"rate_text"`
	AcquisitionDate string  `db:"acquisition_date" json:"acquisition_date"`
	MaturityDate    string  `db:"maturity_date" json:"maturity_date"`
	PrincipalAmount float64 `db:"principal_amount" json:"principal_amount"`
	AmortizedCost   float64 `db:"amortized_cost" json:"amortized_cost"`
	FairValue       float64 `db:"fair_value" json:"fair_value"`
	PctNetAssets    float64 `db:"pct_net_assets" json:"pct_net_assets"`
	NonAccrual      int     `db:"non_accrual" json:"non_accrual"`
	Footnotes       string  `db:"footnotes" json:"footnotes"`
	SourceFormat    string  `db:"source_format" json:"source_format"`
}

// FacilityRow represents a row in the bdc_facility_timeseries table produced
// by the SQL calculation stages.
type FacilityRow struct {
	BDCTicker            string   `db:"bdc_ticker" json:"bdc_ticker"`
	Manager              string   `db:"manager" json:"manager"`
	CanonicalBorrower    string   `db:"canonical_borrower" json:"canonical_borrower"`
	BorrowerName         string   `db:"borrower_name" json:"borrower_name"`
	InvestmentType       string   `db:"investment_type" json:"investment_type"`
	MaturityDate         string   `db:"maturity_date" json:"maturity_date"`
	ReportDate           string   `db:"report_date" json:"report_date"`
	FilingDate           string   `db:"filing_date" json:"filing_date"`
	Form                 string   `db:"form" json:"form"`
	AccessionNumber      string   `db:"accession_number" json:"accession_number"`
	RowNum               int      `db:"row_num" json:"row_num"`
	IsSPV                int      `db:"is_spv" json:"is_spv"`
	MatchTier            string   `db:"match_tier" json:"match_tier"`
	MatchedKeyword       string   `db:"matched_keyword" json:"matched_keyword"`
	Industry             string   `db:"industry" json:"industry"`
	ReferenceRate        string   `db:"reference_rate" json:"reference_rate"`
	SpreadBps            float64  `db:"spread_bps" json:"spread_bps"`
	PrevSpreadBps        *float64 `db:"prev_spread_bps" json:"prev_spread_bps,omitempty"`
	SpreadChangeBps      *float64 `db:"spread_change_bps" json:"spread_change_bps,omitempty"`
	InterestRatePct      float64  `db:"interest_rate_pct" json:"interest_rate_pct"`
	PrevInterestRatePct  *float64 `db:"prev_interest_rate_pct" json:"prev_interest_rate_pct,omitempty"`
	RateChangePct        *float64 `db:"rate_change_pct" json:"rate_change_pct,omitempty"`
	PIKRatePct           float64  `db:"pik_rate_pct" json:"pik_rate_pct"`
	PrincipalAmount      float64  `db:"principal_amount" json:"principal_amount"`
	AmortizedCost        float64  `db:"amortized_cost" json:"amortized_cost"`
	FairValue            float64  `db:"fair_value" json:"fair_value"`
	FVToCostRatio        *float64 `db:"fv_to_cost_ratio" json:"fv_to_cost_ratio,omitempty"`
	PrevFVToCostRatio    *float64 `db:"prev_fv_to_cost_ratio" json:"prev_fv_to_cost_ratio,omitempty"`
	FVToCostChange       *float64 `db:"fv_to_cost_change" json:"fv_to_cost_change,omitempty"`
	FVToParRatio         *float64 `db:"fv_to_par_ratio" json:"fv_to_par_ratio,omitempty"`
	MarkdownUSD          float64  `db:"markdown_usd" json:"markdown_usd"`
	MarkdownPct          float64  `db:"markdown_pct" json:"markdown_pct"`
	PrevMarkdownPct      *float64 `db:"prev_markdown_pct" json:"prev_markdown_pct,omitempty"`
	MarkdownChangePct    *float64 `db:"markdown_change_pct" json:"markdown_change_pct,omitempty"`
	NonAccrual           int      `db:"non_accrual" json:"non_accrual"`
	PrevNonAccrual       *int     `db:"prev_non_accrual" json:"prev_non_accrual,omitempty"`
	NonAccrualTransition string   `db:"non_accrual_transition" json:"non_accrual_transition"`
	DistressFlag         int      `db:"distress_flag" json:"distress_flag"`
}

// AlertRow represents a credit distress alert in bdc_credit_alerts.
type AlertRow struct {
	ReportDate           string   `db:"report_date" json:"report_date"`
	FilingDate           string   `db:"filing_date" json:"filing_date"`
	BDCTicker            string   `db:"bdc_ticker" json:"bdc_ticker"`
	Manager              string   `db:"manager" json:"manager"`
	CanonicalBorrower    string   `db:"canonical_borrower" json:"canonical_borrower"`
	BorrowerName         string   `db:"borrower_name" json:"borrower_name"`
	IsSPV                int      `db:"is_spv" json:"is_spv"`
	MatchTier            string   `db:"match_tier" json:"match_tier"`
	InvestmentType       string   `db:"investment_type" json:"investment_type"`
	MaturityDate         string   `db:"maturity_date" json:"maturity_date"`
	AlertReason          string   `db:"alert_reason" json:"alert_reason"`
	SpreadBps            float64  `db:"spread_bps" json:"spread_bps"`
	SpreadChangeBps      *float64 `db:"spread_change_bps" json:"spread_change_bps,omitempty"`
	InterestRatePct      float64  `db:"interest_rate_pct" json:"interest_rate_pct"`
	AmortizedCost        float64  `db:"amortized_cost" json:"amortized_cost"`
	FairValue            float64  `db:"fair_value" json:"fair_value"`
	FVToCostRatio        *float64 `db:"fv_to_cost_ratio" json:"fv_to_cost_ratio,omitempty"`
	MarkdownPct          float64  `db:"markdown_pct" json:"markdown_pct"`
	MarkdownChangePct    *float64 `db:"markdown_change_pct" json:"markdown_change_pct,omitempty"`
	NonAccrual           int      `db:"non_accrual" json:"non_accrual"`
	NonAccrualTransition string   `db:"non_accrual_transition" json:"non_accrual_transition"`
	AccessionNumber      string   `db:"accession_number" json:"accession_number"`
	RowNum               int      `db:"row_num" json:"row_num"`
}

// AnalysisSummary summarizes the SQL slice-table analytical pass.
type AnalysisSummary struct {
	FilingsCount      int `json:"filings_count"`
	RawSOIRows        int `json:"raw_soi_rows"`
	TechInfraRows     int `json:"tech_infra_rows"`
	GPUSPVRows        int `json:"gpu_spv_rows"`
	NonAccrualRows    int `json:"non_accrual_rows"`
	CreditAlertsCount int `json:"credit_alerts_count"`
}

// Config controls BDC EDGAR ingestion and SQL analysis.
type Config struct {
	DBPath     string   // Target SQLite database path (default: appenv.BDCDB())
	BDCs       []string // Tickers, managers, or CIKs (empty = all DefaultBDCs)
	Since      string   // Earliest report/filing date YYYY-MM-DD (default: "2023-01-01")
	MaxFilings int      // Max 10-Q/10-K filings per BDC to ingest (0 = all since Since)
	LocalDir   string   // Optional local directory of downloaded EDGAR XML/HTML filings
	Refresh    bool     // Re-download and re-parse filings already present in bdc_filings
	DryRun     bool     // Preview filings that would be ingested without writing to DB
	CalcDir    string   // Optional directory to keep scratch calc DB for inspection
	UserAgent  string   // SEC EDGAR User-Agent header (default: appenv.SECUserAgent())
	Out        io.Writer
}

// Result reports the outcome of an Ingest run.
type Result struct {
	FilingsDiscovered int             `json:"filings_discovered"`
	FilingsIngested   int             `json:"filings_ingested"`
	FilingsSkipped    int             `json:"filings_skipped"`
	RowsExtracted     int             `json:"rows_extracted"`
	Summary           AnalysisSummary `json:"summary"`
}

// QueryOptions filters rows returned from bdc_facility_timeseries and bdc_credit_alerts.
type QueryOptions struct {
	DBPath         string
	BDC            string // Filter by BDC ticker or manager
	Search         string // Substring match on canonical_borrower, borrower_name, industry, or matched_keyword
	Tier           string // "gpu_spv", "tech_infra", or "" for both
	SPVOnly        bool   // Only rows where is_spv = 1
	NonAccrualOnly bool   // Only rows where non_accrual = 1
	AlertsOnly     bool   // Query bdc_credit_alerts or distress_flag = 1
	Since          string // Minimum report_date YYYY-MM-DD
	Limit          int    // Max rows (0 = 250)
}
