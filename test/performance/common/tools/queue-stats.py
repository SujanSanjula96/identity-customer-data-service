#!/usr/bin/env python3
"""Summarize the queue monitor data (queue-monitor.sh) for one time window.

Usage:
    queue-stats.py <queue.csv> <start epoch> <end epoch> [<warm-up seconds>] [--header]

The window is one test level. The rows in the first <warm-up seconds> of the
window are not in the rates. The drain part after the window (until the queue is
empty) is in the drain columns.

Output: one CSV line with these columns:
    enqueue_per_sec    jobs that came into the queue for each second (measured part)
    dequeue_per_sec    jobs that the CDS workers took for each second (measured part)
    worker_per_sec     dequeue rate in the intervals where the queue had a backlog at
                       both ends. This is the capacity of the workers. Empty if the
                       queue had no backlog.
    max_queue_size     the largest queue depth in the window
    end_queue_size     the queue depth at the end of the window
    queue_growth_per_sec   the slope of the queue depth (least squares) in the measured
                       part. A value near 0 means a stable queue. A positive value means
                       that the jobs come faster than the workers take them.
    drain_seconds      the time from the end of the window until the queue is empty.
                       Empty if the queue did not become empty in the data.
"""

import csv
import sys

COLUMNS = ["enqueue_per_sec", "dequeue_per_sec", "worker_per_sec", "max_queue_size",
           "end_queue_size", "queue_growth_per_sec", "drain_seconds"]


def read_rows(path):
    rows = []
    with open(path, newline="") as handle:
        for row in csv.DictReader(handle):
            try:
                rows.append((int(row["epoch_seconds"]), int(row["queue_size"]),
                             int(row["enqueue_count"]), int(row["dequeue_count"])))
            except (KeyError, ValueError):
                continue  # A failed read of the console.
    return rows


def rate(first, last, index):
    seconds = last[0] - first[0]
    return (last[index] - first[index]) / seconds if seconds > 0 else 0.0


def slope(points):
    if len(points) < 2:
        return 0.0
    n = len(points)
    mean_x = sum(p[0] for p in points) / n
    mean_y = sum(p[1] for p in points) / n
    den = sum((p[0] - mean_x) ** 2 for p in points)
    if den == 0:
        return 0.0
    return sum((p[0] - mean_x) * (p[1] - mean_y) for p in points) / den


def fmt(value):
    return "" if value is None else (f"{value:.2f}" if isinstance(value, float) else str(value))


def main():
    args = [a for a in sys.argv[1:] if a != "--header"]
    if "--header" in sys.argv[1:] and not args:
        print(",".join(COLUMNS))
        return 0
    if len(args) not in (3, 4):
        print(__doc__, file=sys.stderr)
        return 2
    path, start, end = args[0], int(args[1]), int(args[2])
    warmup = int(float(args[3])) if len(args) == 4 else 0

    rows = read_rows(path)
    window = [r for r in rows if start <= r[0] <= end]
    measured = [r for r in window if r[0] >= start + warmup]
    if len(measured) < 2:
        print(f"Not enough queue data between {start} and {end} in {path}", file=sys.stderr)
        return 1

    first, last = measured[0], measured[-1]
    busy_seconds = 0
    busy_jobs = 0
    for a, b in zip(measured, measured[1:]):
        if a[1] > 0 and b[1] > 0:
            busy_seconds += b[0] - a[0]
            busy_jobs += b[3] - a[3]
    worker = busy_jobs / busy_seconds if busy_seconds > 0 else None

    drain = None
    for r in rows:
        if r[0] >= end and r[1] == 0:
            drain = r[0] - end
            break

    if "--header" in sys.argv[1:]:
        print(",".join(COLUMNS))
    print(",".join(fmt(v) for v in [
        rate(first, last, 2),
        rate(first, last, 3),
        worker,
        max(r[1] for r in window),
        window[-1][1],
        slope([(r[0], r[1]) for r in measured]),
        drain,
    ]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
