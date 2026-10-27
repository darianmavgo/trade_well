-- Compute quarter-over-quarter spread changes, valuation changes, and non-accrual transitions.
INSERT INTO bdc_slice_transitions (
    accession_number, bdc_ticker, manager, form, filing_date, report_date, row_num,
    canonical_borrower, borrower_name, is_spv, match_tier, matched_keyword,
    industry, investment_type, maturity_date, reference_rate,
    spread_bps, prev_spread_bps, spread_change_bps,
    interest_rate_pct, prev_interest_rate_pct, rate_change_pct,
    pik_rate_pct, principal_amount, amortized_cost, fair_value,
    fv_to_cost_ratio, prev_fv_to_cost_ratio, fv_to_cost_change,
    fv_to_par_ratio, markdown_usd, markdown_pct, prev_markdown_pct, markdown_change_pct,
    non_accrual, prev_non_accrual, non_accrual_transition, distress_flag, alert_reason
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
    prev_spread_bps,
    CASE WHEN prev_spread_bps IS NOT NULL AND spread_bps > 0 AND prev_spread_bps > 0 THEN ROUND(spread_bps - prev_spread_bps, 2) ELSE NULL END,
    interest_rate_pct,
    prev_interest_rate_pct,
    CASE WHEN prev_interest_rate_pct IS NOT NULL AND interest_rate_pct > 0 AND prev_interest_rate_pct > 0 THEN ROUND(interest_rate_pct - prev_interest_rate_pct, 4) ELSE NULL END,
    pik_rate_pct,
    principal_amount,
    amortized_cost,
    fair_value,
    fv_to_cost_ratio,
    prev_fv_to_cost_ratio,
    CASE WHEN prev_fv_to_cost_ratio IS NOT NULL AND fv_to_cost_ratio IS NOT NULL THEN ROUND(fv_to_cost_ratio - prev_fv_to_cost_ratio, 6) ELSE NULL END,
    fv_to_par_ratio,
    markdown_usd,
    markdown_pct,
    prev_markdown_pct,
    CASE WHEN prev_markdown_pct IS NOT NULL THEN ROUND(markdown_pct - prev_markdown_pct, 4) ELSE NULL END,
    non_accrual,
    prev_non_accrual,
    CASE
        WHEN non_accrual = 1 AND COALESCE(prev_non_accrual, 0) = 0 THEN 'NEW_NON_ACCRUAL'
        WHEN non_accrual = 1 AND prev_non_accrual = 1 THEN 'ONGOING_NON_ACCRUAL'
        WHEN non_accrual = 0 AND prev_non_accrual = 1 THEN 'CURED_NON_ACCRUAL'
        ELSE 'PERFORMING'
    END,
    CASE
        WHEN non_accrual = 1 THEN 1
        WHEN markdown_pct <= -3.0 THEN 1
        WHEN prev_markdown_pct IS NOT NULL AND (markdown_pct - prev_markdown_pct) <= -2.0 THEN 1
        WHEN prev_spread_bps IS NOT NULL AND spread_bps > 0 AND prev_spread_bps > 0 AND (spread_bps - prev_spread_bps) >= 50.0 THEN 1
        ELSE 0
    END,
    TRIM(
        (CASE WHEN non_accrual = 1 AND COALESCE(prev_non_accrual, 0) = 0 THEN 'NEW_NON_ACCRUAL '
              WHEN non_accrual = 1 THEN 'NON_ACCRUAL ' ELSE '' END) ||
        (CASE WHEN markdown_pct <= -3.0 THEN 'FV_MARKDOWN ' ELSE '' END) ||
        (CASE WHEN prev_markdown_pct IS NOT NULL AND (markdown_pct - prev_markdown_pct) <= -2.0 THEN 'QOQ_MARKDOWN_WIDENING ' ELSE '' END) ||
        (CASE WHEN prev_spread_bps IS NOT NULL AND spread_bps > 0 AND prev_spread_bps > 0 AND (spread_bps - prev_spread_bps) >= 50.0 THEN 'SPREAD_WIDENING' ELSE '' END)
    )
FROM bdc_slice_lag;
