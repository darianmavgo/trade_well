-- Entries from the saved decision tree of the signal symbol (__SYMBOL__). The
-- tree is trained by train tree and read from the tree model database, attached
-- here. The features for the symbol are already in decision_tree_features_slice,
-- built by the tree_features stage before this pipeline runs.
ATTACH DATABASE '__TREE_DB__' AS treemodel;

DROP TABLE IF EXISTS tree_leaf;
CREATE TABLE tree_leaf (date TEXT, pred TEXT);

DROP TABLE IF EXISTS tree_strategy_signals;
CREATE TABLE tree_strategy_signals (
    idx INTEGER,
    symbol TEXT,
    date TEXT,
    open REAL,
    high REAL,
    low REAL,
    close REAL,
    volume INTEGER,
    buylimit REAL,
    entry INTEGER,
    direction TEXT,
    regime TEXT,
    hold_days_override INTEGER,
    take_profit REAL,
    stop_loss REAL,
    allocation_pct_override REAL
);
