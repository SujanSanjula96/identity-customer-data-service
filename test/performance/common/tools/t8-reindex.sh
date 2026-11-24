#!/usr/bin/env bash
# Test case T8: rule activation and reindex. perf-test-cds.sh runs this script
# in the script mode, after it restores the seed snapshot.
#
# Steps:
#   1. Make an active exact rule (EMAIL) on traits.alt_email. Measure the backfill.
#   2. Make an active fuzzy rule (NAME) on traits.alt_name. Measure the backfill.
#   3. Run T1 with the users of -u for -d minutes. After -b minutes, make the
#      exact rule again (step 1). Compare the T1 response times during the
#      backfill with the T1 response times before it.
#   4. Set the fuzzy rule inactive. Measure the index removal.
#
# Output in the directory of -R:
#   t8-reindex.jsonl   one JSON line for each backfill and removal
#   t8-load.csv        the T1 summary before and during the backfill of step 3
#
# If a backfill stops before it completes, the CDS log has an "ABORTED" line.
# The JSON lines show this in "complete" and "aborted_in_log".

set -uo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
jmeter_dir="$(cd "$script_dir/../jmeter" && pwd)"
results=""
props=""
cds_host="localhost" cds_port=8900 is_host="localhost" is_port=9443 org="carbon.super"
users=100
load_minutes=10
before_minutes=3

while getopts "R:q:H:P:I:Q:o:u:d:b:h" opt; do
    case "$opt" in
        R) results="$OPTARG" ;;
        q) props="$OPTARG" ;;
        H) cds_host="$OPTARG" ;;
        P) cds_port="$OPTARG" ;;
        I) is_host="$OPTARG" ;;
        Q) is_port="$OPTARG" ;;
        o) org="$OPTARG" ;;
        u) users="$OPTARG" ;;
        d) load_minutes="$OPTARG" ;;
        b) before_minutes="$OPTARG" ;;
        h) sed -n '2,20p' "$0"; exit 0 ;;
        *) exit 2 ;;
    esac
done
[ -n "$results" ] || { echo "Give the results directory with -R." >&2; exit 2; }
mkdir -p "$results"
out="$results/t8-reindex.jsonl"
: >"$out"
admin() { python3 "$script_dir/cds-admin.py" "$@"; }
rc=0

echo "# T8 step 1: backfill of an exact rule"
admin reindex perf-alt-email traits.alt_email EMAIL deterministic | tee -a "$out" || rc=1
echo "# T8 step 2: backfill of a fuzzy rule"
admin reindex perf-alt-name traits.alt_name NAME fuzzy | tee -a "$out" || rc=1

echo "# T8 step 3: backfill during T1 load ($users users, $load_minutes min)"
jtl="$results/t1-under-load.jtl"
rm -f "$jtl"
jmeter -n -t "$jmeter_dir/profile/Anonymous_Visitor_Profile_Creation.jmx" ${props:+-q "$props"} \
    -Jhost="$cds_host" -Jport="$cds_port" -JisHost="$is_host" -JisPort="$is_port" -Jorg="$org" \
    -Jconcurrency="$users" -JrampUpPeriod=10 -Jtime=$((load_minutes * 60)) \
    -Jjmeter.save.saveservice.output_format=csv \
    -Jhttpclient.reset_state_on_thread_group_iteration=false \
    -l "$jtl" -j "$results/t1-under-load.log" >"$results/t1-under-load.out" 2>&1 &
jmeter_pid=$!
sleep $((before_minutes * 60))
backfill_start="$(date +%s)"
line="$(admin reindex perf-alt-email traits.alt_email EMAIL deterministic)" || rc=1
backfill_end="$(date +%s)"
echo "$line" | sed 's/^{/{"under_load": true, /' | tee -a "$out"
wait "$jmeter_pid" || rc=1

# The T1 samples before the backfill (after a 60 s ramp-up) and during it.
python3 - "$jtl" "$backfill_start" "$backfill_end" >"$results/t8-load.csv" <<'PY' || rc=1
import csv, math, sys
path, b_start, b_end = sys.argv[1], int(sys.argv[2]) * 1000, int(sys.argv[3]) * 1000
rows = [(int(r["timeStamp"]), int(r["elapsed"]), r["success"] == "true")
        for r in csv.DictReader(open(path)) if r["label"] == "1 Create Profile"]
first = min(r[0] for r in rows)
def stats(part):
    if not part:
        return ["0", "", "", "", ""]
    e = sorted(r[1] for r in part)
    n = len(e)
    span = (max(r[0] for r in part) - min(r[0] for r in part)) / 1000 or 1
    return [str(n), f"{100 * sum(1 for r in part if not r[2]) / n:.2f}", f"{sum(e) / n:.2f}",
            str(e[max(1, math.ceil(0.95 * n)) - 1]), f"{n / span:.2f}"]
print("window,samples,error_percent,average_ms,p95_ms,throughput_per_sec")
print("before_backfill," + ",".join(stats([r for r in rows if first + 60000 <= r[0] < b_start])))
print("during_backfill," + ",".join(stats([r for r in rows if b_start <= r[0] < b_end])))
PY
cat "$results/t8-load.csv"

echo "# T8 step 4: removal of the fuzzy rule index"
admin deactivate perf-alt-name traits.alt_name | tee -a "$out" || rc=1
exit "$rc"
