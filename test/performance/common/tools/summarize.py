#!/usr/bin/env python3
"""Summarize one JMeter result file (CSV .jtl) for one sample label.

The samples in the warm-up period are ignored. The warm-up period starts at the
first sample of the label.

Usage:
    summarize.py <results.jtl> <label> <warmup_seconds> [--header]

The label can have the form <label>@<column>. Then the value of that column is
the time of the sample in place of the elapsed time, for example
"5 Time To Merge@mergeMs" (a JMeter variable in sample_variables). A failed
sample has no value and counts as an error.

Output: one CSV line with these columns:
    samples, error_percent, average_ms, stddev_ms, p95_ms, p99_ms, throughput_per_sec
"""

import csv
import math
import sys

COLUMNS = ["samples", "error_percent", "average_ms", "stddev_ms", "p95_ms", "p99_ms",
           "throughput_per_sec"]


def percentile(sorted_values, fraction):
    """Nearest-rank percentile, the method that JMeter uses."""
    if not sorted_values:
        return 0
    rank = max(1, math.ceil(fraction * len(sorted_values)))
    return sorted_values[rank - 1]


def main():
    args = [a for a in sys.argv[1:] if a != "--header"]
    if "--header" in sys.argv[1:] and not args:
        print(",".join(COLUMNS))
        return 0
    if len(args) != 3:
        print(__doc__, file=sys.stderr)
        return 2

    path, label, warmup_seconds = args[0], args[1], float(args[2])
    column = None
    if "@" in label:
        label, column = label.rsplit("@", 1)

    rows = []
    with open(path, newline="") as handle:
        for row in csv.DictReader(handle):
            if row.get("label") != label:
                continue
            try:
                start = int(row["timeStamp"])
                elapsed = int(row["elapsed"])
            except (KeyError, ValueError):
                continue
            success = row.get("success", "").lower() == "true"
            if column is not None:
                try:
                    elapsed = int(float(row.get(column) or ""))
                except ValueError:
                    elapsed, success = 0, False
            rows.append((start, elapsed, success))

    if not rows:
        print(f"No samples with the label '{label}' in {path}", file=sys.stderr)
        return 1

    first = min(r[0] for r in rows)
    cutoff = first + warmup_seconds * 1000
    measured = [r for r in rows if r[0] >= cutoff]
    if not measured:
        print("No samples after the warm-up period. Use a longer test or a shorter warm-up.",
              file=sys.stderr)
        return 1

    elapsed = sorted(r[1] for r in measured)
    count = len(elapsed)
    errors = sum(1 for r in measured if not r[2])
    average = sum(elapsed) / count
    stddev = math.sqrt(sum((e - average) ** 2 for e in elapsed) / count)
    window_ms = max(r[0] + r[1] for r in measured) - min(r[0] for r in measured)
    throughput = count / (window_ms / 1000) if window_ms > 0 else 0

    if "--header" in sys.argv[1:]:
        print(",".join(COLUMNS))
    print(",".join([
        str(count),
        f"{100 * errors / count:.2f}",
        f"{average:.2f}",
        f"{stddev:.2f}",
        str(percentile(elapsed, 0.95)),
        str(percentile(elapsed, 0.99)),
        f"{throughput:.2f}",
    ]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
