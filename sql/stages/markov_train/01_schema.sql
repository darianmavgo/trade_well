-- Slice tables of one training batch, in the scratch calc database. Each file
-- after this one reads the table the one before it wrote.
DROP TABLE IF EXISTS markov_ret;
CREATE TABLE markov_ret (symbol TEXT, date TEXT, close REAL, ret_20d REAL);

DROP TABLE IF EXISTS markov_state;
CREATE TABLE markov_state (symbol TEXT, date TEXT, close REAL, ret_20d REAL, state INTEGER);

DROP TABLE IF EXISTS markov_transition;
CREATE TABLE markov_transition (symbol TEXT, date TEXT, from_state INTEGER, to_state INTEGER);

DROP TABLE IF EXISTS markov_cumulative;
CREATE TABLE markov_cumulative (
    symbol TEXT, date TEXT, from_state INTEGER,
    cum_to_bull INTEGER, cum_to_bear INTEGER, cum_to_sideways INTEGER, total_transitions INTEGER
);

DROP TABLE IF EXISTS markov_batch_prediction;
CREATE TABLE markov_batch_prediction (
    symbol TEXT, date TEXT, state INTEGER, prob_bull REAL, prob_bear REAL, signal REAL
);
