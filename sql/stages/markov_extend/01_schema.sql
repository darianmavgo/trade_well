-- Slice tables of one extend pass, in the scratch calc database. The saved model is
-- attached as model and is not rewritten until 06. Each file reads the table before it.
DROP TABLE IF EXISTS markov_x_ret;
CREATE TABLE markov_x_ret (symbol TEXT, date TEXT, close REAL, ret_20d REAL);

DROP TABLE IF EXISTS markov_x_state;
CREATE TABLE markov_x_state (symbol TEXT, date TEXT, state INTEGER);

DROP TABLE IF EXISTS markov_x_count;
CREATE TABLE markov_x_count (symbol TEXT, state INTEGER, to_bull INTEGER, to_bear INTEGER, total INTEGER);

DROP TABLE IF EXISTS markov_x_new;
CREATE TABLE markov_x_new (symbol TEXT, date TEXT, state INTEGER, prob_bull REAL, prob_bear REAL, signal REAL);

DROP TABLE IF EXISTS markov_x_meta;
CREATE TABLE markov_x_meta (symbol TEXT, added INTEGER, last_date TEXT)
