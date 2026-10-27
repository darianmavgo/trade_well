-- Slice tables of one HMM input build, in the scratch calc database. Each file after this
-- one reads the table the one before it wrote. The bars come from the attached daily bars
-- database (bars.daily_bar), never from the hourly market database.
DROP TABLE IF EXISTS hmm_ret;
CREATE TABLE hmm_ret (symbol TEXT, date TEXT, ret REAL);

DROP TABLE IF EXISTS hmm_roll;
CREATE TABLE hmm_roll (symbol TEXT, date TEXT, ret REAL, n INTEGER, mean_r REAL, mean_r2 REAL);

DROP TABLE IF EXISTS hmm_obs;
CREATE TABLE hmm_obs (symbol TEXT, date TEXT, ret REAL, vol REAL);
