#!/usr/bin/env bash
# Runs the CDS performance test scenarios on the bastion (JMeter) node and
# packs the results. start-performance.sh starts this script with nohup, so
# that a lost SSH connection does not stop the tests. The script writes its
# exit code to ~/cds-perf/run.done at the end.
#
# All options after "--" go to common/jmeter/perf-test-cds.sh, for example
# -c "50 100" -d 15 -w 5 -i t1_,t2_.
#
# Before the tests, the script makes the seed profiles for T5 to T8 (-S, one
# time for each stack) and saves a snapshot of the CDS database. The scenarios
# restore the snapshot. During the tests, tools/queue-monitor.sh records the
# unification queue each 10 seconds (results/queue.csv).

set -uo pipefail

bundle_dir="$HOME/cds-perf"
results_dir="$bundle_dir/results"
metadata=""
seed_count=100000
tools="$bundle_dir/perf/common/tools"

while getopts "m:S:h" opt; do
    case "$opt" in
        m) metadata="$OPTARG" ;;
        S) seed_count="$OPTARG" ;;
        h) echo "Usage: $0 [-m <metadata json>] [-S <seed profiles, 0 for none>] -- [perf-test-cds.sh options]"; exit 0 ;;
        *) exit 2 ;;
    esac
done
shift $((OPTIND - 1))
[ "${1:-}" = "--" ] && shift

rm -f "$bundle_dir/run.done"
rm -rf "$results_dir"
mkdir -p "$results_dir"
[ -n "$metadata" ] && cp "$metadata" "$results_dir/test-metadata.json"

# shellcheck source=/dev/null
source "$bundle_dir/secrets.env"
cds_db_count() {
    PGPASSWORD="$CDS_DB_PASS" psql -h "$CDS_DB_HOST" -U "$CDS_DB_USER" -d cds_db -tAc \
        "SELECT json_build_object('time', now(), 'profiles', (SELECT count(*) FROM profiles),
                'profile_reference', (SELECT count(*) FROM profile_reference),
                'profile_cookies', (SELECT count(*) FROM profile_cookies),
                'blocking_keys', (SELECT count(*) FROM blocking_keys),
                'database_bytes', pg_database_size('cds_db'))" 2>/dev/null
}

# The plan records the profile count at the start and at the end of each run.
cds_db_count >"$results_dir/db-before.json"
PGPASSWORD="$CDS_DB_PASS" psql -h "$CDS_DB_HOST" -U "$CDS_DB_USER" -d cds_db -qc \
    'SELECT pg_stat_statements_reset()' >/dev/null 2>&1 || true

# The queue monitor runs until this script ends.
export QUEUE_CSV="$results_dir/queue.csv"
"$tools/queue-monitor.sh" -o "$QUEUE_CSV" -s "$bundle_dir/secrets.env" >"$results_dir/queue-monitor.log" 2>&1 &
monitor_pid=$!
trap 'kill "$monitor_pid" 2>/dev/null' EXIT

# The sync scenarios use the CDS sync user.
export CDS_SYNC_USER CDS_SYNC_PASS SEED_COUNT="$seed_count"
export SNAPSHOT="$bundle_dir/seed-$seed_count.dump"
if [ "$seed_count" -gt 0 ] && [ ! -s "$SNAPSHOT" ]; then
    echo "# Seed: $seed_count profiles, then a snapshot of the CDS database"
    {
        python3 "$tools/cds-admin.py" schema &&
            python3 "$tools/cds-admin.py" rules &&
            python3 "$tools/data-gen.py" seed "$seed_count" "$bundle_dir/seed.csv" &&
            python3 "$tools/cds-admin.py" load-seed "$bundle_dir/seed.csv" &&
            python3 "$tools/cds-admin.py" wait-queue &&
            python3 "$tools/cds-admin.py" snapshot "$SNAPSHOT.tmp" &&
            mv "$SNAPSHOT.tmp" "$SNAPSHOT"
    } >"$results_dir/seed.log" 2>&1 || { echo "The seed failed. See $results_dir/seed.log" >&2; cat "$results_dir/seed.log" >&2; }
    cat "$results_dir/seed.log"
fi

date -u +%Y-%m-%dT%H:%M:%SZ >"$results_dir/start-time.txt"
"$bundle_dir/perf/common/jmeter/perf-test-cds.sh" \
    -H cdslb -P 443 -I wso2is -Q 9443 \
    -s "$bundle_dir/state.env" -R "$results_dir" "$@" >"$results_dir/perf-test-cds.out" 2>&1
rc=$?
date -u +%Y-%m-%dT%H:%M:%SZ >"$results_dir/end-time.txt"

cds_db_count >"$results_dir/db-after.json"
PGPASSWORD="$CDS_DB_PASS" psql -h "$CDS_DB_HOST" -U "$CDS_DB_USER" -d cds_db --csv -c \
    "SELECT calls, round(total_exec_time::numeric, 1) AS total_ms, round(mean_exec_time::numeric, 3) AS mean_ms,
            rows, left(regexp_replace(query, '\s+', ' ', 'g'), 300) AS query
       FROM pg_stat_statements ORDER BY total_exec_time DESC LIMIT 50" \
    >"$results_dir/pg-stat-statements.csv" 2>/dev/null || true

(cd "$bundle_dir" && rm -f results.zip && zip -qr results.zip results)
echo "$rc" >"$bundle_dir/run.done"
exit "$rc"
