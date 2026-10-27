-- What each strategy family's row columns mean to a grid search. One row per
-- (family, column). gridsearchable = 1 means gridsearch varies the column and
-- grid_values is the JSON list it tries (the row's own value is always added so
-- the baseline is in the grid). gridsearchable = 0 means gridsearch leaves it
-- as the row has it, and why_not says why. kind is int, real or text. role is
-- identity, label, symbol, entry, exit, sizing, cost, stat or legacy (a column no code reads any more).
CREATE TABLE IF NOT EXISTS strategy_family_param (
    family TEXT NOT NULL,
    param TEXT NOT NULL,
    kind TEXT NOT NULL,
    role TEXT NOT NULL,
    gridsearchable INTEGER NOT NULL,
    grid_values TEXT,
    why_not TEXT,
    PRIMARY KEY (family, param)
);
