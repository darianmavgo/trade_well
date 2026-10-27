INSERT INTO regime_coverage (what, n) SELECT 'trades loaded', COUNT(*) FROM trade_all;
INSERT INTO regime_coverage (what, n) SELECT 'duplicates dropped', COUNT(*) FROM trade_ranked WHERE rn > 1;
INSERT INTO regime_coverage (what, n) SELECT 'trades scored', COUNT(*) FROM trade_regime;
INSERT INTO regime_coverage (what, n) SELECT 'unlabeled (no earlier session)', COUNT(*) FROM trade_regime WHERE regime = 'unlabeled'
