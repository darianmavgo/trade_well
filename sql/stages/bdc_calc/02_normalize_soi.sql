-- Normalize current-period Schedule of Investments rows and join filing metadata.
-- Converts decimal rates (for example 0.105) to percentage points (10.5) and decimal spreads (0.055) to bps (550).
INSERT INTO bdc_slice_normalized (
    accession_number, bdc_ticker, manager, form, filing_date, report_date, row_num,
    borrower_name, borrower_upper, industry, industry_upper, investment_type, inv_type_upper,
    reference_rate, spread_bps, interest_rate_pct, pik_rate_pct, floor_rate_pct, rate_text,
    acquisition_date, maturity_date, principal_amount, amortized_cost, fair_value, non_accrual, footnotes
)
SELECT
    r.accession_number,
    r.bdc_ticker,
    COALESCE(f.manager, ''),
    r.form,
    r.filing_date,
    r.report_date,
    r.row_num,
    TRIM(r.borrower_name),
    UPPER(TRIM(r.borrower_name)),
    TRIM(r.industry),
    UPPER(TRIM(r.industry)),
    TRIM(r.investment_type),
    UPPER(TRIM(r.investment_type)),
    TRIM(r.reference_rate),
    CASE
        WHEN r.spread_bps > 0 AND r.spread_bps < 1.0 THEN ROUND(r.spread_bps * 10000.0, 2)
        WHEN r.spread_bps >= 1.0 AND r.spread_bps < 35.0 THEN ROUND(r.spread_bps * 100.0, 2)
        ELSE ROUND(r.spread_bps, 2)
    END,
    CASE
        WHEN r.interest_rate_pct > 0 AND r.interest_rate_pct < 1.0 THEN ROUND(r.interest_rate_pct * 100.0, 4)
        ELSE ROUND(r.interest_rate_pct, 4)
    END,
    CASE
        WHEN r.pik_rate_pct > 0 AND r.pik_rate_pct < 1.0 THEN ROUND(r.pik_rate_pct * 100.0, 4)
        ELSE ROUND(r.pik_rate_pct, 4)
    END,
    CASE
        WHEN r.floor_rate_pct > 0 AND r.floor_rate_pct < 1.0 THEN ROUND(r.floor_rate_pct * 100.0, 4)
        ELSE ROUND(r.floor_rate_pct, 4)
    END,
    TRIM(r.rate_text),
    TRIM(r.acquisition_date),
    TRIM(r.maturity_date),
    r.principal_amount,
    r.amortized_cost,
    r.fair_value,
    CASE
        WHEN r.non_accrual = 1 THEN 1
        WHEN UPPER(r.footnotes) LIKE '%NON-ACCRUAL%' OR UPPER(r.footnotes) LIKE '%NONACCRUAL%' THEN 1
        WHEN UPPER(r.rate_text) LIKE '%NON-ACCRUAL%' OR UPPER(r.rate_text) LIKE '%NONACCRUAL%' THEN 1
        ELSE 0
    END,
    TRIM(r.footnotes)
FROM bdc.bdc_soi_raw r
LEFT JOIN bdc.bdc_filings f ON f.accession_number = r.accession_number
WHERE (r.period_date = '' OR r.period_date = r.report_date)
  AND TRIM(r.borrower_name) <> ''
  AND (r.amortized_cost <> 0 OR r.fair_value <> 0 OR r.principal_amount <> 0);
