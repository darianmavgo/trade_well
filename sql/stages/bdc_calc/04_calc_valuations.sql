-- Compute fair-value-to-cost, fair-value-to-par, and dollar/percentage markdowns
-- for all GPU SPV and tech/infrastructure investments.
INSERT INTO bdc_slice_valuations (
    accession_number, bdc_ticker, manager, form, filing_date, report_date, row_num,
    canonical_borrower, borrower_name, is_spv, match_tier, matched_keyword,
    industry, investment_type, reference_rate, spread_bps, interest_rate_pct,
    pik_rate_pct, floor_rate_pct, rate_text, acquisition_date, maturity_date,
    principal_amount, amortized_cost, fair_value,
    fv_to_cost_ratio, fv_to_par_ratio, markdown_usd, markdown_pct,
    non_accrual, footnotes
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
    reference_rate,
    spread_bps,
    interest_rate_pct,
    pik_rate_pct,
    floor_rate_pct,
    rate_text,
    acquisition_date,
    maturity_date,
    principal_amount,
    amortized_cost,
    fair_value,
    CASE WHEN amortized_cost > 0 THEN ROUND(fair_value / amortized_cost, 6) ELSE NULL END,
    CASE WHEN principal_amount > 0 THEN ROUND(fair_value / principal_amount, 6) ELSE NULL END,
    CASE WHEN amortized_cost > 0 THEN ROUND(fair_value - amortized_cost, 2) ELSE 0 END,
    CASE WHEN amortized_cost > 0 THEN ROUND(((fair_value - amortized_cost) / amortized_cost) * 100.0, 4) ELSE 0 END,
    non_accrual,
    footnotes
FROM bdc_slice_classified
WHERE match_tier IN ('gpu_spv', 'tech_infra');
