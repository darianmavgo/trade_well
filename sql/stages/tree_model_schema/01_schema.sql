-- The persisted decision tree, one row per node, addressed the way CloudForest
-- addresses nodes: path is the string of L and R turns from the root, so the
-- root is the empty string. An inner node has a feature and a threshold and
-- sends a row left when the feature is at or below the threshold. A leaf has pred,
-- the 5 bucket class it predicts for the next bar's return: -2 (at or below -5
-- percent), -1, 0, 1 or 2 (at or above 5 percent). Written by train tree and
-- read by backtest.
CREATE TABLE IF NOT EXISTS tree_node (
    symbol TEXT NOT NULL,
    path TEXT NOT NULL,
    feature TEXT,
    threshold REAL,
    pred TEXT,
    PRIMARY KEY (symbol, path)
) WITHOUT ROWID;

-- One row per trained symbol: what the tree was fit on and when. class_counts is
-- a JSON object of labelled bars per class. grown_cases is how many bars the tree
-- was grown on after the neutral class was downsampled.
CREATE TABLE IF NOT EXISTS tree_model_meta (
    symbol TEXT PRIMARY KEY,
    samples INTEGER NOT NULL,
    grown_cases INTEGER NOT NULL,
    class_counts TEXT NOT NULL,
    first_date TEXT NOT NULL,
    last_date TEXT NOT NULL,
    trained_at TEXT NOT NULL
);
