-- Calendar year return of each curve, in percent: the table the command prints.
DROP TABLE IF EXISTS clone_yearly;
CREATE TABLE clone_yearly (curve TEXT, yr TEXT, n INTEGER, total_pct REAL);
INSERT INTO clone_yearly SELECT 'bench', substr(date, 1, 4), COUNT(*), ROUND((EXP(SUM(LN(1 + r))) - 1) * 100, 1) FROM bench GROUP BY 2;
INSERT INTO clone_yearly SELECT 'top3', substr(date, 1, 4), COUNT(*), ROUND((EXP(SUM(LN(1 + r))) - 1) * 100, 1) FROM p_top3 GROUP BY 2;
INSERT INTO clone_yearly SELECT 'top5', substr(date, 1, 4), COUNT(*), ROUND((EXP(SUM(LN(1 + r))) - 1) * 100, 1) FROM p_top5 GROUP BY 2;
INSERT INTO clone_yearly SELECT 'top10', substr(date, 1, 4), COUNT(*), ROUND((EXP(SUM(LN(1 + r))) - 1) * 100, 1) FROM p_top10 GROUP BY 2;
INSERT INTO clone_yearly SELECT 'top20', substr(date, 1, 4), COUNT(*), ROUND((EXP(SUM(LN(1 + r))) - 1) * 100, 1) FROM p_top20 GROUP BY 2;
INSERT INTO clone_yearly SELECT 'top10_gated', substr(date, 1, 4), COUNT(*), ROUND((EXP(SUM(LN(1 + r))) - 1) * 100, 1) FROM g_top10 GROUP BY 2;
INSERT INTO clone_yearly SELECT 'top20_gated', substr(date, 1, 4), COUNT(*), ROUND((EXP(SUM(LN(1 + r))) - 1) * 100, 1) FROM g_top20 GROUP BY 2;
INSERT INTO clone_yearly SELECT 'qqq', substr(date, 1, 4), COUNT(*), ROUND((EXP(SUM(LN(1 + r))) - 1) * 100, 1) FROM q GROUP BY 2;
