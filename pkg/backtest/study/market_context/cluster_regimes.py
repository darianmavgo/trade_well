"""Cluster market-context days from 20-session log returns.

Each date is one observation. The default features are the 20-session log
returns of VOO, IEF, GLD, USO, and HYG. Pass --features to drop some, and
--out to write a new database instead of replacing cluster_day in --db.
KMeans is fit for k = 2..10 on standardized features; the k with the
highest silhouette score is kept (never more than 10).
"""

import argparse
import sqlite3

import numpy as np
import pandas as pd
from sklearn.cluster import KMeans
from sklearn.metrics import silhouette_score
from sklearn.preprocessing import StandardScaler

ALL_TICKERS = ["VOO", "IEF", "GLD", "USO", "HYG"]
DEFAULT_DB = "/Users/darianhickman/Documents/backtestgosqlite/reports/market_context_20d.db"
MAX_K = 10
RANDOM_STATE = 0


def load_features(db_path, tickers):
    placeholders = ",".join("?" for _ in tickers)
    with sqlite3.connect(db_path) as conn:
        df = pd.read_sql_query(
            f"""
            SELECT date, ticker, return_20d_log, return_20d_simple
            FROM return_20d
            WHERE return_20d_log IS NOT NULL
              AND ticker IN ({placeholders})
            """,
            conn,
            params=tickers,
        )
    wide_log = df.pivot(index="date", columns="ticker", values="return_20d_log")
    wide_simple = df.pivot(index="date", columns="ticker", values="return_20d_simple")
    wide_log = wide_log.reindex(columns=tickers).dropna()
    wide_simple = wide_simple.reindex(index=wide_log.index, columns=tickers)
    return wide_log, wide_simple


def choose_k(X):
    rows = []
    models = {}
    for k in range(2, MAX_K + 1):
        model = KMeans(n_clusters=k, n_init=10, random_state=RANDOM_STATE)
        labels = model.fit_predict(X)
        sil = float(silhouette_score(X, labels))
        rows.append({"k": k, "inertia": float(model.inertia_), "silhouette": sil})
        models[k] = (model, labels)
    best = max(rows, key=lambda r: r["silhouette"])
    return best["k"], rows, models


def save(db_path, scores, chosen_k, labels, dates, simple, tickers):
    score_df = pd.DataFrame(scores)
    score_df["chosen"] = (score_df["k"] == chosen_k).astype(int)
    day = pd.DataFrame({"date": dates, "cluster": labels})
    grouped = simple.copy()
    grouped["cluster"] = labels
    n = grouped.groupby("cluster").size().rename("n")
    means = grouped.groupby("cluster")[tickers].mean()
    center_rows = means.reset_index().melt(id_vars="cluster", var_name="ticker", value_name="mean_return_20d_simple")
    center_rows = center_rows.merge(n.reset_index(), on="cluster")
    features = pd.DataFrame({"ticker": tickers})

    with sqlite3.connect(db_path) as conn:
        features.to_sql("cluster_features", conn, if_exists="replace", index=False)
        score_df.to_sql("cluster_k_score", conn, if_exists="replace", index=False)
        center_rows.to_sql("cluster_center", conn, if_exists="replace", index=False)
        day.to_sql("cluster_day", conn, if_exists="replace", index=False)
        conn.execute("CREATE INDEX IF NOT EXISTS idx_cluster_day_date ON cluster_day(date)")


def held_out_simple(db_path, dates, ticker):
    with sqlite3.connect(db_path) as conn:
        df = pd.read_sql_query(
            """
            SELECT date, return_20d_simple
            FROM return_20d
            WHERE ticker = ? AND return_20d_simple IS NOT NULL
            """,
            conn,
            params=[ticker],
        )
    s = df.set_index("date")["return_20d_simple"]
    return s.reindex(dates)


def main():
    parser = argparse.ArgumentParser(description="Cluster 20-session market-context days")
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--out", default="", help="Write cluster tables here. Default is --db.")
    parser.add_argument("--features", default=",".join(ALL_TICKERS), help="Comma-separated tickers used as features")
    args = parser.parse_args()
    tickers = [t.strip().upper() for t in args.features.split(",") if t.strip()]
    if len(tickers) < 2:
        raise SystemExit("need at least two feature tickers")

    wide_log, wide_simple = load_features(args.db, tickers)
    if len(wide_log) < MAX_K + 1:
        raise SystemExit(f"only {len(wide_log)} complete dates; need more than {MAX_K}")

    scaler = StandardScaler()
    X = scaler.fit_transform(wide_log.to_numpy())
    chosen_k, scores, models = choose_k(X)
    _, labels = models[chosen_k]
    # Number clusters by the friendliest feature mean: VOO when it is in the
    # fit, otherwise HYG, otherwise the first feature.
    sort_name = "VOO" if "VOO" in tickers else ("HYG" if "HYG" in tickers else tickers[0])
    order = (
        pd.Series(wide_simple[sort_name].to_numpy(), index=labels)
        .groupby(level=0)
        .mean()
        .sort_values(ascending=False)
    )
    remap = {old: new for new, old in enumerate(order.index)}
    labels = np.array([remap[c] for c in labels])

    out_path = args.out or args.db
    save(out_path, scores, chosen_k, labels, wide_log.index.to_numpy(), wide_simple, tickers)

    print(f"features: {', '.join(tickers)}")
    print(f"dates: {len(wide_log)}  {wide_log.index.min()} .. {wide_log.index.max()}")
    print(f"chosen k={chosen_k} (highest silhouette, k<=10)")
    for row in scores:
        mark = "  <--" if row["k"] == chosen_k else ""
        print(f"  k={row['k']:2d}  silhouette={row['silhouette']:.4f}  inertia={row['inertia']:.1f}{mark}")

    profile = wide_simple.copy()
    profile["cluster"] = labels
    if "VOO" not in tickers:
        profile["VOO_held_out"] = held_out_simple(args.db, wide_log.index, "VOO").to_numpy()
    print("\nmean 20-session simple return by cluster")
    cols = tickers + (["VOO_held_out"] if "VOO" not in tickers else [])
    summary = profile.groupby("cluster")[cols].mean()
    summary.insert(0, "n", profile.groupby("cluster").size())
    print(summary.to_string(float_format=lambda v: f"{v: .3f}"))
    if out_path != args.db:
        print_overlap(args.db, wide_log.index, labels)
    print(f"\nwrote cluster_features, cluster_k_score, cluster_center, cluster_day to {out_path}")


def print_overlap(source_db, dates, labels):
    """How the new labels sit on cluster_day already stored in the source db."""
    with sqlite3.connect(source_db) as conn:
        old = pd.read_sql_query("SELECT date, cluster FROM cluster_day", conn)
    if old.empty:
        return
    cur = pd.DataFrame({"date": dates, "new": labels})
    both = cur.merge(old, on="date", how="inner")
    if both.empty:
        return
    table = pd.crosstab(both["cluster"], both["new"], margins=True)
    print("\noverlap with the existing cluster_day (rows = previous cluster, columns = this fit)")
    print(table.to_string())


if __name__ == "__main__":
    main()
