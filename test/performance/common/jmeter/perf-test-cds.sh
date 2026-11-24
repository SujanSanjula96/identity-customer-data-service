#!/usr/bin/env bash
# Runs the CDS performance test scenarios with Apache JMeter.
#
# Each scenario runs one time for each load level. The samples in the warm-up
# period are not in the summary. The structure follows
# https://github.com/wso2/performance-is (common/jmeter/perf-test-is.sh).
#
# The test cases T1 to T8 are in test/performance/README.md. T5 to T8 need the seed profiles and the database snapshot
# that run-performance-tests.sh makes (-S there).

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
perf_dir="$(cd "$script_dir/../.." && pwd)"
tools="$perf_dir/common/tools"

# --------------------------------------------------------------------------- #
# Scenarios. One line for each scenario, with these fields:
#   name           a unique name. -i and -e select scenarios by a part of it.
#   display name
#   jmx file       relative to this directory. For the script mode: a command.
#   labels         the measured sample labels, separated by ";". A label of the
#                  form <label>@<column> uses that result column as the time.
#   mode           users: the levels are concurrent users (-c).
#                  rate: the levels are arrival rates in jobs for each second (-t).
#                  script: run the command one time. No JMeter levels.
#   prepare        cds-admin.py commands before the scenario, separated by ";".
#                  "restore" restores the seed snapshot.
#   data           "incoming:<exact %>:<fuzzy %>": a new data file for each level
#                  (data-gen.py incoming). Empty: no data file.
# To add a scenario, add a line.
# --------------------------------------------------------------------------- #
scenarios=(
  "t1_anonymous_profile_creation|T1 Anonymous Profile Creation|profile/Anonymous_Visitor_Profile_Creation.jmx|1 Create Profile|users||"
  "t2_profile_update|T2 Profile Update|profile/Profile_Update.jmx|1 Update Profile|users||"
  "t3_registration_sync|T3 Registration Sync|sync/Registration_Sync.jmx|1 Registration Sync|users||"
  "t4_login_sync|T4 Login Sync|sync/Login_Sync.jmx|3 Login Sync;5 Time To Merge@mergeMs|users||"
  "t5_profile_link|T5 Profile Link (exact rules)|profile/Profile_Link.jmx|3 Link Profile|users|restore;ruleset R1|incoming:0:0"
  "t6_profile_link_fuzzy|T6 Profile Link (fuzzy rules)|profile/Profile_Link.jmx|3 Link Profile|users|restore;ruleset R4|incoming:0:20"
  "t7_unification_r1|T7 Unification R1 (1 exact)|unification/Unification_Load.jmx|1 Create Profile|rate|restore;ruleset R1|incoming:20:20"
  "t7_unification_r2|T7 Unification R2 (1 fuzzy)|unification/Unification_Load.jmx|1 Create Profile|rate|restore;ruleset R2|incoming:20:20"
  "t7_unification_r3|T7 Unification R3 (1 exact, 1 fuzzy)|unification/Unification_Load.jmx|1 Create Profile|rate|restore;ruleset R3|incoming:20:20"
  "t7_unification_r4|T7 Unification R4 (2 exact, 2 fuzzy)|unification/Unification_Load.jmx|1 Create Profile|rate|restore;ruleset R4|incoming:20:20"
  "t7_unification_r5|T7 Unification R5 (3 exact, 3 fuzzy)|unification/Unification_Load.jmx|1 Create Profile|rate|restore;ruleset R5|incoming:20:20"
  "t7_unification_r4_not_decisive|T7 Unification R4, deterministic_match_decisive off|unification/Unification_Load.jmx|1 Create Profile|rate|restore;ruleset R4;decisive false|incoming:20:20"
  "t8_rule_activation_reindex|T8 Rule Activation and Reindex|t8-reindex.sh||script|restore;ruleset R5|"
)

# --------------------------------------------------------------------------- #
# Defaults
# --------------------------------------------------------------------------- #
concurrent_users="50 100 150 300 500"
arrival_rates="50 100 250 500 1000 drain"
duration_minutes=15
warmup_minutes=5
ramp_up_seconds=""
cds_host="localhost"
cds_port=8900
is_host="localhost"
is_port=9443
org="carbon.super"
state_file=""
results_dir="$perf_dir/results"
include_scenarios=""
exclude_scenarios=""
jmeter_heap="2g"
seed_count="${SEED_COUNT:-100000}"
queue_wait_minutes=30
drain_rate="${DRAIN_RATE:-2000}"
drain_seconds="${DRAIN_SECONDS:-30}"

usage() {
  cat <<EOF
Usage: $0 [options]

  -c <users>     Concurrent users for the users scenarios, in quotes. Default: "$concurrent_users".
  -t <rates>     Arrival rates (jobs for each second) for the rate scenarios, in
                 quotes. The value "drain" sends $drain_rate jobs for each second for
                 $drain_seconds s, then measures the time until the queue is empty.
                 Default: "$arrival_rates".
  -d <minutes>   Test duration for each level. Decimals are permitted. Default: $duration_minutes.
  -w <minutes>   Warm-up time, not in the summary. Decimals are permitted. Default: $warmup_minutes.
  -r <seconds>   Ramp-up time. Default: 10 % of the warm-up time, minimum 1.
  -H <host>      CDS host. Default: $cds_host.
  -P <port>      CDS port. Default: $cds_port.
  -I <host>      Identity server host. Default: $is_host.
  -Q <port>      Identity server port. Default: $is_port.
  -o <org>       Organization handle. Default: $org.
  -s <file>      File with CLIENT_ID and CLIENT_SECRET lines, for example the
                 state.env of scripts/local-setup/script.sh.
  -i <names>     Run only the scenarios whose name contains one of these texts,
                 separated by commas. For example: -i t1_,t2_,t3_
  -e <names>     Do not run the scenarios whose name contains one of these texts.
  -n <count>     The seed profile count, for the data of T5 to T7. Default: $seed_count.
  -q <minutes>   After each level, wait at most this time for an empty unification
                 queue. Default: $queue_wait_minutes.
  -m <heap>      JMeter heap size. Default: $jmeter_heap.
  -R <dir>       Results directory. Default: $results_dir.
  -l             List the scenarios and exit.
  -h             Show this help and exit.

Credentials: set CLIENT_ID and CLIENT_SECRET in the environment, or use -s.
As an alternative, set CDS_TOKEN to an access token. Then the test does not call
the token endpoint. The sync scenarios (T3, T4) also need CDS_SYNC_USER and
CDS_SYNC_PASS in the environment or in the file of -s.

The queue statistics need QUEUE_CSV (the output of tools/queue-monitor.sh) in
the environment. run-performance-tests.sh sets it.
EOF
}

list_scenarios() {
  local entry name display
  for entry in "${scenarios[@]}"; do
    IFS='|' read -r name display _ <<<"$entry"
    printf '%-36s %s\n' "$name" "$display"
  done
}

while getopts "c:t:d:w:r:H:P:I:Q:o:s:i:e:n:q:m:R:lh" opt; do
  case "$opt" in
    c) concurrent_users="$OPTARG" ;;
    t) arrival_rates="$OPTARG" ;;
    d) duration_minutes="$OPTARG" ;;
    w) warmup_minutes="$OPTARG" ;;
    r) ramp_up_seconds="$OPTARG" ;;
    H) cds_host="$OPTARG" ;;
    P) cds_port="$OPTARG" ;;
    I) is_host="$OPTARG" ;;
    Q) is_port="$OPTARG" ;;
    o) org="$OPTARG" ;;
    s) state_file="$OPTARG" ;;
    i) include_scenarios="$OPTARG" ;;
    e) exclude_scenarios="$OPTARG" ;;
    n) seed_count="$OPTARG" ;;
    q) queue_wait_minutes="$OPTARG" ;;
    m) jmeter_heap="$OPTARG" ;;
    R) results_dir="$OPTARG" ;;
    l) list_scenarios; exit 0 ;;
    h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

command -v jmeter >/dev/null 2>&1 || { echo "jmeter is not installed or not in PATH." >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is not installed or not in PATH." >&2; exit 1; }

# Reads one KEY=value line from the state file. The value is not printed.
state_get() {
  local key="$1" line
  line="$(grep -E "^(export )?${key}=" "$state_file" | tail -n 1 || true)"
  line="${line#export }"
  line="${line#*=}"
  line="${line%\"}"; line="${line#\"}"
  line="${line%\'}"; line="${line#\'}"
  printf '%s' "$line"
}

if [ -n "$state_file" ]; then
  [ -r "$state_file" ] || { echo "Cannot read the state file: $state_file" >&2; exit 1; }
  CLIENT_ID="${CLIENT_ID:-$(state_get CLIENT_ID)}"
  CLIENT_SECRET="${CLIENT_SECRET:-$(state_get CLIENT_SECRET)}"
  CDS_SYNC_USER="${CDS_SYNC_USER:-$(state_get CDS_SYNC_USER)}"
  CDS_SYNC_PASS="${CDS_SYNC_PASS:-$(state_get CDS_SYNC_PASS)}"
fi

if [ -z "${CDS_TOKEN:-}" ] && { [ -z "${CLIENT_ID:-}" ] || [ -z "${CLIENT_SECRET:-}" ]; }; then
  echo "No credentials. Set CLIENT_ID and CLIENT_SECRET, or use -s <state file>, or set CDS_TOKEN." >&2
  exit 1
fi

to_seconds() { awk -v m="$1" 'BEGIN { printf "%d", m * 60 }'; }
duration_seconds="$(to_seconds "$duration_minutes")"
warmup_seconds="$(to_seconds "$warmup_minutes")"
if [ "$duration_seconds" -le "$warmup_seconds" ]; then
  echo "The test duration must be longer than the warm-up time." >&2
  exit 1
fi
if [ -z "$ramp_up_seconds" ]; then
  ramp_up_seconds=$(( warmup_seconds / 10 ))
  [ "$ramp_up_seconds" -ge 1 ] || ramp_up_seconds=1
fi

# The secrets go to JMeter in a properties file, not on the command line, so
# that they do not show in the process list.
secrets_file="$(mktemp)"
chmod 600 "$secrets_file"
data_file=""
trap 'rm -f "$secrets_file" ${data_file:+"$data_file"}' EXIT
{
  [ -n "${CLIENT_ID:-}" ] && printf 'clientId=%s\n' "$CLIENT_ID"
  [ -n "${CLIENT_SECRET:-}" ] && printf 'clientSecret=%s\n' "$CLIENT_SECRET"
  [ -n "${CDS_TOKEN:-}" ] && printf 'token=%s\n' "$CDS_TOKEN"
  [ -n "${CDS_SYNC_USER:-}" ] && printf 'syncUser=%s\n' "$CDS_SYNC_USER"
  [ -n "${CDS_SYNC_PASS:-}" ] && printf 'syncPass=%s\n' "$CDS_SYNC_PASS"
  true
} >"$secrets_file"

mkdir -p "$results_dir"
summary_file="$results_dir/summary.csv"
queue_file="$results_dir/queue-summary.csv"
counts_file="$results_dir/unification-counts.jsonl"
printf 'scenario,level,label,%s\n' "$(python3 "$tools/summarize.py" --header)" >"$summary_file"
printf 'scenario,level,%s\n' "$(python3 "$tools/queue-stats.py" --header)" >"$queue_file"
# The database work of each level (pg_stat_statements before and after the level, with the
# queue drain). The file stays empty if the runner has no access to the CDS database.
db_file="$results_dir/db-by-level.csv"
printf 'scenario,level,%s\n' "$(python3 "$tools/pg-stats-diff.py" --header)" >"$db_file"
: >"$counts_file"

export HEAP="-Xms${jmeter_heap} -Xmx${jmeter_heap}"

# matches LIST NAME - true if NAME contains one of the comma-separated texts.
matches() {
  local item
  local IFS=','
  for item in $1; do
    [ -n "$item" ] && [[ "$2" == *"$item"* ]] && return 0
  done
  return 1
}

# wait_for_queue - waits until the unification queue is empty, at most
# queue_wait_minutes. Returns 1 on a time-out.
wait_for_queue() {
  [ -n "${QUEUE_CSV:-}" ] || return 0
  local limit=$(( $(date +%s) + queue_wait_minutes * 60 )) size
  sleep 15
  while [ "$(date +%s)" -lt "$limit" ]; do
    size="$(tail -n 1 "$QUEUE_CSV" | cut -d, -f2)"
    [ "$size" = "0" ] && return 0
    sleep 10
  done
  return 1
}

# JMeter closes the connections of a thread at the start of each iteration by
# default. The callers of CDS are applications that keep their connections, and
# a new TLS connection for each request uses all the client ports of the load
# generator. Thus the runs below keep the connections open.

failed=0
for entry in "${scenarios[@]}"; do
  IFS='|' read -r name display jmx labels mode prepare data <<<"$entry"

  if [ -n "$include_scenarios" ] && ! matches "$include_scenarios" "$name"; then continue; fi
  if [ -n "$exclude_scenarios" ] && matches "$exclude_scenarios" "$name"; then continue; fi

  echo "## $display"
  # Each scenario starts with an empty unification queue (see cmd_purge_queue).
  python3 "$tools/cds-admin.py" purge-queue || true
  if [ -n "$prepare" ]; then
    IFS=';' read -r -a steps <<<"$prepare"
    for step in "${steps[@]}"; do
      # shellcheck disable=SC2086  # The step has a command and its arguments.
      if [ "$step" = "restore" ]; then
        python3 "$tools/cds-admin.py" restore "${SNAPSHOT:?Set SNAPSHOT to the seed snapshot}" || { failed=1; continue 2; }
      else
        python3 "$tools/cds-admin.py" $step || { failed=1; continue 2; }
      fi
    done
    # The rules cache of CDS keeps a value for 30 seconds.
    sleep 35
  fi

  if [ "$mode" = "script" ]; then
    run_dir="$results_dir/$name"
    mkdir -p "$run_dir"
    if ! "$tools/$jmx" -R "$run_dir" -q "$secrets_file" -H "$cds_host" -P "$cds_port" \
        -I "$is_host" -Q "$is_port" -o "$org" >"$run_dir/run.out" 2>&1; then
      echo "  The script failed. See $run_dir/run.out" >&2
      failed=1
    fi
    cat "$run_dir/run.out"
    continue
  fi

  if [ "$mode" = "rate" ]; then levels="$arrival_rates"; else levels="$concurrent_users"; fi
  level_index=0
  for level in $levels; do
    level_index=$((level_index + 1))
    run_dir="$results_dir/$name/$level"
    rm -rf "$run_dir"
    mkdir -p "$run_dir"
    time_s="$duration_seconds" warm_s="$warmup_seconds" ramp_s="$ramp_up_seconds"
    load_args=()
    if [ "$mode" = "rate" ]; then
      rate="$level"
      if [ "$level" = "drain" ]; then
        rate="$drain_rate" time_s="$drain_seconds" warm_s=0 ramp_s=1
      fi
      # Enough threads for the rate when a request takes up to 200 ms.
      threads=$(( rate / 5 ))
      [ "$threads" -ge 20 ] || threads=20
      [ "$threads" -le 1000 ] || threads=1000
      load_args=(-Jrate="$rate" -Jconcurrency="$threads")
      echo "# $display: $level jobs/s, $time_s s (warm-up $warm_s s)"
    else
      load_args=(-Jconcurrency="$level")
      echo "# $display: $level users, $time_s s (warm-up $warm_s s)"
    fi

    data_args=()
    if [ -n "$data" ]; then
      IFS=':' read -r _ exact_pct fuzzy_pct <<<"$data"
      rows=$(( ${rate:-100} * time_s + 10000 ))
      # A users level makes at most about 20 iterations for each user and second.
      # The data set starts again at the end of the file (recycle), so 2 million
      # rows are enough.
      [ "$mode" = "users" ] && rows=$(( level * time_s * 20 + 10000 ))
      [ "$rows" -le 2000000 ] || rows=2000000
      data_file="$results_dir/.data-$name-$level.csv"
      # Each level gets new values (a new offset), so that a level does not
      # match the profiles of the level before it.
      python3 "$tools/data-gen.py" incoming "$seed_count" $(( level_index * 10000000 )) "$rows" \
        "$data_file" "$exact_pct" "$fuzzy_pct"
      data_args=(-JdataFile="$data_file")
    fi

    python3 "$tools/cds-admin.py" pg-stats "$run_dir/.pg-stats-before.csv" >/dev/null 2>&1 \
      || rm -f "$run_dir/.pg-stats-before.csv"
    level_start="$(date +%s)"
    if ! jmeter -n -t "$script_dir/$jmx" \
      -q "$secrets_file" \
      -Jhost="$cds_host" -Jport="$cds_port" \
      -JisHost="$is_host" -JisPort="$is_port" \
      -Jorg="$org" \
      "${load_args[@]}" ${data_args[@]+"${data_args[@]}"} \
      -JrampUpPeriod="$ramp_s" -Jtime="$time_s" \
      -Jjmeter.save.saveservice.output_format=csv \
      -Jsample_variables=mergeMs \
      -Jhttpclient.reset_state_on_thread_group_iteration=false \
      -l "$run_dir/results.jtl" -j "$run_dir/jmeter.log" >"$run_dir/jmeter.out" 2>&1; then
      echo "  JMeter failed. See $run_dir/jmeter.out" >&2
      failed=1
      continue
    fi
    level_end="$(date +%s)"

    IFS=';' read -r -a label_list <<<"$labels"
    for label in "${label_list[@]}"; do
      if line="$(python3 "$tools/summarize.py" "$run_dir/results.jtl" "$label" "$warm_s")"; then
        printf '%s,%s,"%s",%s\n' "$name" "$level" "${label%@*}" "$line" >>"$summary_file"
        echo "  ${label%@*}: $line"
      else
        echo "  No summary for $label. See $run_dir/jmeter.log" >&2
        failed=1
      fi
    done

    # Many scenarios send unification jobs (T3 to T7). A backlog from one level
    # must not slow the next level or the next scenario, so wait for an empty
    # queue after each level.
    if [ -n "${QUEUE_CSV:-}" ]; then
      if ! wait_for_queue; then
        echo "  The queue is not empty after $queue_wait_minutes minutes." >&2
      fi
      if q="$(python3 "$tools/queue-stats.py" "$QUEUE_CSV" "$level_start" "$level_end" "$warm_s")"; then
        printf '%s,%s,%s\n' "$name" "$level" "$q" >>"$queue_file"
        echo "  queue: $q"
      fi
    fi
    if [ -n "$data" ]; then
      python3 "$tools/cds-admin.py" counts "$level_start" "$(date +%s)" \
        | sed "s/^{/{\"scenario\": \"$name\", \"level\": \"$level\", /" >>"$counts_file" || true
      tail -n 1 "$counts_file" | sed 's/^/  counts: /'
    fi
    if [ -s "$run_dir/.pg-stats-before.csv" ] &&
      python3 "$tools/cds-admin.py" pg-stats "$run_dir/.pg-stats-after.csv" >/dev/null 2>&1 &&
      d="$(python3 "$tools/pg-stats-diff.py" "$run_dir/.pg-stats-before.csv" \
        "$run_dir/.pg-stats-after.csv" "$run_dir/pg-stat-statements.csv")"; then
      printf '%s,%s,%s\n' "$name" "$level" "$d" >>"$db_file"
      echo "  database: $d"
    fi
    rm -f "$run_dir/.pg-stats-before.csv" "$run_dir/.pg-stats-after.csv"
    [ -z "$data_file" ] || rm -f "$data_file"
    data_file=""
  done
done

echo
echo "Summary: $summary_file"
column -s, -t "$summary_file" 2>/dev/null || cat "$summary_file"
exit "$failed"
