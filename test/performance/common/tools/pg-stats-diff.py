#!/usr/bin/env python3
"""Compare two pg_stat_statements files of cds-admin.py pg-stats (before and after one level).

Usage:
    pg-stats-diff.py <before.csv> <after.csv> <out.csv> [--header]

The values in pg_stat_statements add up for the life of the database. The difference between
the two files is the database work of the level, including the queue drain after it.

<out.csv> gets the 50 statements with the most execution time in the level, with the same
columns as pg-stat-statements.csv of the run, plus share_pct (the part of the level time).

Output: one CSV line with these columns:
    exec_ms        the execution time of all statements in the level
    calls          the number of statement calls in the level
    blks_read      the blocks that the statements read from disk (not from the buffer cache)
    blks_hit       the blocks that the statements found in the buffer cache
    top_share_pct  the part of exec_ms of the most expensive statement
    top_query      the first 80 characters of that statement

A statement that is only in <after.csv> counts in full: it started in the level. This happens
after a snapshot restore, because the restored tables get new IDs and thus new query IDs.
"""

import csv
import sys

COLUMNS = ["exec_ms", "calls", "blks_read", "blks_hit", "top_share_pct", "top_query"]


def load(path):
    rows = {}
    with open(path, newline="") as h:
        for r in csv.DictReader(h):
            rows[r["queryid"]] = r
    return rows


def main(argv):
    if "--header" in argv:
        print(",".join(COLUMNS))
        return 0
    if len(argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    before, after = load(argv[0]), load(argv[1])

    # Join by the query text, because one text can have more than one query ID.
    by_query = {}
    for qid, r in after.items():
        b = before.get(qid, {})
        calls = int(r["calls"]) - int(b.get("calls", 0) or 0)
        # The pg-stats query of the runner is not part of the test.
        if calls <= 0 or "pg_stat_statements" in r["query"]:
            continue
        d = by_query.setdefault(r["query"], [0, 0.0, 0, 0, 0])
        d[0] += calls
        d[1] += float(r["total_ms"]) - float(b.get("total_ms", 0) or 0)
        d[2] += int(r["rows"]) - int(b.get("rows", 0) or 0)
        d[3] += int(r["shared_blks_hit"]) - int(b.get("shared_blks_hit", 0) or 0)
        d[4] += int(r["shared_blks_read"]) - int(b.get("shared_blks_read", 0) or 0)

    total_ms = sum(d[1] for d in by_query.values())
    top = sorted(by_query.items(), key=lambda kv: -kv[1][1])
    with open(argv[2], "w", newline="") as h:
        w = csv.writer(h)
        w.writerow(["calls", "total_ms", "mean_ms", "rows", "blks_hit", "blks_read", "share_pct",
                    "query"])
        for q, d in top[:50]:
            w.writerow([d[0], round(d[1], 1), round(d[1] / d[0], 3), d[2], d[3], d[4],
                        round(100 * d[1] / total_ms, 1) if total_ms else 0, q])

    top_q, top_d = top[0] if top else ("", [0, 0.0, 0, 0, 0])
    line = [round(total_ms), sum(d[0] for d in by_query.values()),
            sum(d[4] for d in by_query.values()), sum(d[3] for d in by_query.values()),
            round(100 * top_d[1] / total_ms, 1) if total_ms else 0,
            '"' + top_q[:80].replace('"', "'") + '"']
    print(",".join(str(v) for v in line))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
