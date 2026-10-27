-- Attach prior-quarter metrics per BDC + borrower + investment type + maturity date using a flat window.
INSERT INTO bdc_slice_lag (
    accession_number, bdc_ticker, manager, form, filing_date, report_date, row_num,
    canonical_borrower, borrower_name, is_spv, match_tier, matched_keyword,
    industry, investment_type, maturity_date, reference_rate,
    spread_bps, prev_spread_bps, interest_rate_pct, prev_interest_rate_pct,
    pik_rate_pct, principal_amount, amortized_cost, fair_value,
    fv_to_cost_ratio, prev_fv_to_cost_ratio, fv_to_par_ratio,
    markdown_usd, markdown_pct, prev_markdown_pct, non_accrual, prev_non_accrual
)
SELECT
    accession_number,
    bdc_ticker,
    manager,
    form,
    filing_date,
    report_date,
    row_num,
    canonical_borrower,
    borrower_name,
    is_spv,
    match_tier,
    matched_keyword,
    industry,
    investment_type,
    maturity_date,
    reference_rate,
    spread_bps,
    LAG(spread_bps) OVER w,
    interest_rate_pct,
    LAG(interest_rate_pct) OVER w,
    pik_rate_pct,
    principal_amount,
    amortized_cost,
    fair_value,
    fv_to_cost_ratio,
    LAG(fv_to_cost_ratio) OVER w,
    fv_to_par_ratio,
    markdown_usd,
    markdown_pct,
    LAG(markdown_pct) OVER w,
    non_accrual,
    LAG(non_accrual) OVER w
FROM bdc_slice_valuations
WINDOW w AS (
    PARTITION BY bdc_ticker, borrower_name, investment_type, maturity_date
    ORDER BY report_date, accession_number, row_num
);
