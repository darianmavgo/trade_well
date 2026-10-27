-- Publish the calculated slice tables into the persistent bdc database.
DELETE FROM bdc.bdc_tech_investments;
INSERT INTO bdc.bdc_tech_investments (
    accession_number, bdc_ticker, manager, form, filing_date, report_date, row_num,
    canonical_borrower, borrower_name, is_spv, match_tier, matched_keyword,
    industry, investment_type, reference_rate, spread_bps, interest_rate_pct,
    pik_rate_pct, floor_rate_pct, rate_text, acquisition_date, maturity_date,
    principal_amount, amortized_cost, fair_value,
    fv_to_cost_ratio, fv_to_par_ratio, markdown_usd, markdown_pct,
    non_accrual, footnotes
)
SELECT
    accession_number, bdc_ticker, manager, form, filing_date, report_date, row_num,
    canonical_borrower, borrower_name, is_spv, match_tier, matched_keyword,
    industry, investment_type, reference_rate, spread_bps, interest_rate_pct,
    pik_rate_pct, floor_rate_pct, rate_text, acquisition_date, maturity_date,
    principal_amount, amortized_cost, fair_value,
    fv_to_cost_ratio, fv_to_par_ratio, markdown_usd, markdown_pct,
    non_accrual, footnotes
FROM bdc_slice_valuations;

DELETE FROM bdc.bdc_facility_timeseries;
INSERT INTO bdc.bdc_facility_timeseries (
    bdc_ticker, manager, canonical_borrower, borrower_name, investment_type, maturity_date,
    report_date, filing_date, form, accession_number, row_num,
    is_spv, match_tier, matched_keyword, industry, reference_rate,
    spread_bps, prev_spread_bps, spread_change_bps,
    interest_rate_pct, prev_interest_rate_pct, rate_change_pct,
    pik_rate_pct, principal_amount, amortized_cost, fair_value,
    fv_to_cost_ratio, prev_fv_to_cost_ratio, fv_to_cost_change,
    fv_to_par_ratio, markdown_usd, markdown_pct, prev_markdown_pct, markdown_change_pct,
    non_accrual, prev_non_accrual, non_accrual_transition, distress_flag
)
SELECT
    bdc_ticker, manager, canonical_borrower, borrower_name, investment_type, maturity_date,
    report_date, filing_date, form, accession_number, row_num,
    is_spv, match_tier, matched_keyword, industry, reference_rate,
    spread_bps, prev_spread_bps, spread_change_bps,
    interest_rate_pct, prev_interest_rate_pct, rate_change_pct,
    pik_rate_pct, principal_amount, amortized_cost, fair_value,
    fv_to_cost_ratio, prev_fv_to_cost_ratio, fv_to_cost_change,
    fv_to_par_ratio, markdown_usd, markdown_pct, prev_markdown_pct, markdown_change_pct,
    non_accrual, prev_non_accrual, non_accrual_transition, distress_flag
FROM bdc_slice_transitions;

DELETE FROM bdc.bdc_credit_alerts;
INSERT INTO bdc.bdc_credit_alerts (
    report_date, filing_date, bdc_ticker, manager, canonical_borrower, borrower_name,
    is_spv, match_tier, investment_type, maturity_date, alert_reason,
    spread_bps, spread_change_bps, interest_rate_pct,
    amortized_cost, fair_value, fv_to_cost_ratio, markdown_pct, markdown_change_pct,
    non_accrual, non_accrual_transition, accession_number, row_num
)
SELECT
    report_date, filing_date, bdc_ticker, manager, canonical_borrower, borrower_name,
    is_spv, match_tier, investment_type, maturity_date, alert_reason,
    spread_bps, spread_change_bps, interest_rate_pct,
    amortized_cost, fair_value, fv_to_cost_ratio, markdown_pct, markdown_change_pct,
    non_accrual, non_accrual_transition, accession_number, row_num
FROM bdc_slice_transitions
WHERE distress_flag = 1;
