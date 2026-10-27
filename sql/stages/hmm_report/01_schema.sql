-- Slice tables of the out-of-sample report, in a scratch database. The model database is
-- attached as model and is only read. __SYMBOL_LIST__ is the quoted symbols.
DROP TABLE IF EXISTS hmm_rep_flag;
CREATE TABLE hmm_rep_flag (symbol TEXT, date TEXT, regime INTEGER, in_sample INTEGER, new_run INTEGER);

DROP TABLE IF EXISTS hmm_rep_run;
CREATE TABLE hmm_rep_run (symbol TEXT, date TEXT, regime INTEGER, in_sample INTEGER, run_id INTEGER);

DROP TABLE IF EXISTS hmm_rep_runlen;
CREATE TABLE hmm_rep_runlen (symbol TEXT, regime INTEGER, in_sample INTEGER, run_id INTEGER, len INTEGER);

DROP TABLE IF EXISTS hmm_rep_avgrun;
CREATE TABLE hmm_rep_avgrun (symbol TEXT, regime INTEGER, in_sample INTEGER, avg_run REAL);

DROP TABLE IF EXISTS hmm_rep_regime;
CREATE TABLE hmm_rep_regime (
    symbol TEXT, regime INTEGER, in_sample INTEGER, days INTEGER,
    mean_ret REAL, mean_vol REAL, mean_loglik REAL, avg_run REAL
);
