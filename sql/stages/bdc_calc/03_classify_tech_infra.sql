-- Classify every normalized Schedule of Investments row into GPU/Data-Center SPVs and broader Tech/Infrastructure.
INSERT INTO bdc_slice_classified (
    accession_number, bdc_ticker, manager, form, filing_date, report_date, row_num,
    canonical_borrower, borrower_name, is_spv, match_tier, matched_keyword,
    industry, investment_type, reference_rate, spread_bps, interest_rate_pct,
    pik_rate_pct, floor_rate_pct, rate_text, acquisition_date, maturity_date,
    principal_amount, amortized_cost, fair_value, non_accrual, footnotes
)
SELECT
    accession_number,
    bdc_ticker,
    manager,
    form,
    filing_date,
    report_date,
    row_num,
    CASE
        WHEN borrower_upper LIKE '%COREWEAVE%' THEN 'CoreWeave'
        WHEN borrower_upper LIKE '%CRUSOE%' THEN 'Crusoe Energy / Cloud'
        WHEN borrower_upper LIKE '%LAMBDA%' AND borrower_upper NOT LIKE '%LAMBDA THERAPEUTICS%' THEN 'Lambda Labs'
        WHEN borrower_upper LIKE '%VOLTAGE PARK%' THEN 'Voltage Park'
        WHEN borrower_upper LIKE '%APPLIED DIGITAL%' OR borrower_upper LIKE '%APLD%' THEN 'Applied Digital'
        WHEN borrower_upper LIKE '%TERAWULF%' THEN 'TeraWulf'
        WHEN borrower_upper LIKE '%CORE SCIENTIFIC%' THEN 'Core Scientific'
        WHEN borrower_upper LIKE '%LANCIUM%' THEN 'Lancium'
        WHEN borrower_upper LIKE '%FLUIDSTACK%' THEN 'Fluidstack'
        WHEN borrower_upper LIKE '%TOGETHER COMPUTER%' OR borrower_upper LIKE '%TOGETHER AI%' THEN 'Together AI'
        WHEN borrower_upper LIKE '%NORTHERN DATA%' THEN 'Northern Data'
        WHEN borrower_upper LIKE '%VANTAGE DATA%' THEN 'Vantage Data Centers'
        WHEN borrower_upper LIKE '%QTS%' OR borrower_upper LIKE '%QUALITY TECHNOLOGY SERVICES%' THEN 'QTS Data Centers'
        WHEN borrower_upper LIKE '%SWITCH%' AND (industry_upper LIKE '%DATA%' OR industry_upper LIKE '%TECH%' OR industry_upper LIKE '%INFRA%' OR borrower_upper LIKE '%SWITCH, INC%' OR borrower_upper LIKE '%SWITCH LTD%') THEN 'Switch Data Centers'
        WHEN borrower_upper LIKE '%ALIGNED DATA%' THEN 'Aligned Data Centers'
        WHEN borrower_upper LIKE '%COMPASS DATACENTER%' OR borrower_upper LIKE '%COMPASS DATA CENTER%' THEN 'Compass Datacenters'
        WHEN borrower_upper LIKE '%FLEXENTIAL%' THEN 'Flexential'
        WHEN borrower_upper LIKE '%CYXTERA%' THEN 'Cyxtera Technologies'
        WHEN borrower_upper LIKE '%TIERPOINT%' THEN 'TierPoint'
        WHEN borrower_upper LIKE '%DATABANK%' THEN 'DataBank'
        WHEN borrower_upper LIKE '%COLOGIX%' THEN 'Cologix'
        WHEN borrower_upper LIKE '%EDGECONNEX%' THEN 'EdgeConneX'
        WHEN borrower_upper LIKE '%ROWAN DIGITAL%' THEN 'Rowan Digital Infrastructure'
        WHEN borrower_upper LIKE '%NOVVA%' THEN 'Novva Data Centers'
        WHEN borrower_upper LIKE '%STACK INFRASTRUCTURE%' THEN 'STACK Infrastructure'
        WHEN borrower_upper LIKE '%AIRTRUNK%' THEN 'AirTrunk'
        WHEN borrower_upper LIKE '%YONDR%' THEN 'Yondr Group'
        ELSE borrower_name
    END AS canonical_borrower,
    borrower_name,
    CASE
        WHEN borrower_upper LIKE '%SPV%'
          OR borrower_upper LIKE '%COMPUTE ACQUISITION%'
          OR borrower_upper LIKE '%FUNDING%'
          OR borrower_upper LIKE '%FINANCING%'
          OR borrower_upper LIKE '%BORROWER%'
          OR borrower_upper LIKE '%HOLDINGS%'
          OR borrower_upper LIKE '%FACILITY%'
          OR borrower_upper LIKE '%RECEIVABLES%'
          OR borrower_upper LIKE '%WAREHOUSE%'
          OR inv_type_upper LIKE '%EQUIPMENT%'
          OR inv_type_upper LIKE '%DELAYED DRAW%'
          OR inv_type_upper LIKE '%DDTL%'
        THEN 1
        ELSE 0
    END AS is_spv,
    CASE
        WHEN borrower_upper LIKE '%COREWEAVE%'
          OR borrower_upper LIKE '%CRUSOE%'
          OR (borrower_upper LIKE '%LAMBDA%' AND borrower_upper NOT LIKE '%THERAPEUTICS%')
          OR borrower_upper LIKE '%VOLTAGE PARK%'
          OR borrower_upper LIKE '%APPLIED DIGITAL%'
          OR borrower_upper LIKE '%TERAWULF%'
          OR borrower_upper LIKE '%CORE SCIENTIFIC%'
          OR borrower_upper LIKE '%LANCIUM%'
          OR borrower_upper LIKE '%FLUIDSTACK%'
          OR borrower_upper LIKE '%NORTHERN DATA%'
          OR borrower_upper LIKE '%VANTAGE DATA%'
          OR borrower_upper LIKE '%QUALITY TECHNOLOGY SERVICES%'
          OR borrower_upper LIKE '%ALIGNED DATA%'
          OR borrower_upper LIKE '%COMPASS DATACENTER%'
          OR borrower_upper LIKE '%COMPASS DATA CENTER%'
          OR borrower_upper LIKE '%FLEXENTIAL%'
          OR borrower_upper LIKE '%CYXTERA%'
          OR borrower_upper LIKE '%TIERPOINT%'
          OR borrower_upper LIKE '%DATABANK%'
          OR borrower_upper LIKE '%COLOGIX%'
          OR borrower_upper LIKE '%EDGECONNEX%'
          OR borrower_upper LIKE '%ROWAN DIGITAL%'
          OR borrower_upper LIKE '%NOVVA%'
          OR borrower_upper LIKE '%STACK INFRASTRUCTURE%'
          OR borrower_upper LIKE '%AIRTRUNK%'
          OR borrower_upper LIKE '%YONDR%'
          OR borrower_upper LIKE '%GPU%'
          OR borrower_upper LIKE '%COMPUTE ACQUISITION%'
          OR borrower_upper LIKE '%DATA CENTER%'
          OR borrower_upper LIKE '%DATACENTER%'
          OR industry_upper LIKE '%DATA CENTER%'
          OR industry_upper LIKE '%DATACENTER%'
          OR industry_upper LIKE '%GPU%'
          OR industry_upper LIKE '%HPC%'
        THEN 'gpu_spv'
        WHEN industry_upper LIKE '%SOFTWARE%'
          OR industry_upper LIKE '%TECHNOLOGY%'
          OR industry_upper LIKE '%IT SERVICES%'
          OR industry_upper LIKE '%INFORMATION TECHNOLOGY%'
          OR industry_upper LIKE '%CLOUD%'
          OR industry_upper LIKE '%SEMICONDUCTOR%'
          OR industry_upper LIKE '%INTERNET%'
          OR industry_upper LIKE '%HOSTING%'
          OR industry_upper LIKE '%TELECOMMUNICATIONS%'
          OR industry_upper LIKE '%COMMUNICATIONS EQUIPMENT%'
          OR industry_upper LIKE '%COMPUTER%'
          OR industry_upper LIKE '%CYBER%'
          OR industry_upper LIKE '%ARTIFICIAL INTELLIGENCE%'
          OR borrower_upper LIKE '%CLOUD%'
          OR borrower_upper LIKE '%DIGITAL%'
          OR borrower_upper LIKE '%COMPUTE%'
          OR borrower_upper LIKE '%SEMICONDUCTOR%'
          OR borrower_upper LIKE '%INFRASTRUCTURE%'
          OR borrower_upper LIKE '%SERVER%'
          OR borrower_upper LIKE '%AI %'
        THEN 'tech_infra'
        ELSE 'other'
    END AS match_tier,
    CASE
        WHEN borrower_upper LIKE '%COREWEAVE%' THEN 'CoreWeave'
        WHEN borrower_upper LIKE '%CRUSOE%' THEN 'Crusoe'
        WHEN borrower_upper LIKE '%LAMBDA%' AND borrower_upper NOT LIKE '%THERAPEUTICS%' THEN 'Lambda'
        WHEN borrower_upper LIKE '%VOLTAGE PARK%' THEN 'Voltage Park'
        WHEN borrower_upper LIKE '%APPLIED DIGITAL%' THEN 'Applied Digital'
        WHEN borrower_upper LIKE '%TERAWULF%' THEN 'TeraWulf'
        WHEN borrower_upper LIKE '%CORE SCIENTIFIC%' THEN 'Core Scientific'
        WHEN borrower_upper LIKE '%VANTAGE DATA%' THEN 'Vantage Data Centers'
        WHEN borrower_upper LIKE '%QUALITY TECHNOLOGY SERVICES%' THEN 'QTS'
        WHEN borrower_upper LIKE '%ALIGNED DATA%' THEN 'Aligned Data Centers'
        WHEN borrower_upper LIKE '%COMPASS DATACENTER%' OR borrower_upper LIKE '%COMPASS DATA CENTER%' THEN 'Compass Datacenters'
        WHEN borrower_upper LIKE '%FLEXENTIAL%' THEN 'Flexential'
        WHEN borrower_upper LIKE '%CYXTERA%' THEN 'Cyxtera'
        WHEN borrower_upper LIKE '%TIERPOINT%' THEN 'TierPoint'
        WHEN borrower_upper LIKE '%DATABANK%' THEN 'DataBank'
        WHEN borrower_upper LIKE '%COLOGIX%' THEN 'Cologix'
        WHEN borrower_upper LIKE '%EDGECONNEX%' THEN 'EdgeConneX'
        WHEN borrower_upper LIKE '%STACK INFRASTRUCTURE%' THEN 'STACK Infrastructure'
        WHEN borrower_upper LIKE '%GPU%' OR industry_upper LIKE '%GPU%' THEN 'GPU'
        WHEN borrower_upper LIKE '%COMPUTE ACQUISITION%' THEN 'Compute Acquisition SPV'
        WHEN borrower_upper LIKE '%DATA CENTER%' OR borrower_upper LIKE '%DATACENTER%' OR industry_upper LIKE '%DATA CENTER%' OR industry_upper LIKE '%DATACENTER%' THEN 'Data Center'
        WHEN industry_upper LIKE '%SEMICONDUCTOR%' OR borrower_upper LIKE '%SEMICONDUCTOR%' THEN 'Semiconductors'
        WHEN industry_upper LIKE '%CLOUD%' OR borrower_upper LIKE '%CLOUD%' THEN 'Cloud Infrastructure'
        WHEN industry_upper LIKE '%IT SERVICES%' OR industry_upper LIKE '%INFORMATION TECHNOLOGY%' THEN 'IT Services'
        WHEN industry_upper LIKE '%SOFTWARE%' THEN 'Software'
        WHEN industry_upper LIKE '%TECHNOLOGY%' THEN 'Technology'
        WHEN industry_upper LIKE '%TELECOMMUNICATIONS%' OR industry_upper LIKE '%COMMUNICATIONS%' THEN 'Telecom / Network Infra'
        WHEN industry_upper LIKE '%INTERNET%' OR industry_upper LIKE '%HOSTING%' THEN 'Internet / Hosting'
        ELSE 'Other'
    END AS matched_keyword,
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
    non_accrual,
    footnotes
FROM bdc_slice_normalized;
