#!/usr/bin/env bash
# Records the statistics of the CDS unification queue at a fixed interval.
# Runs on the bastion node in the background during a test. It reads the
# ActiveMQ web console (Jolokia) on port 8161.
#
# Output (CSV): epoch_seconds, queue_size, enqueue_count, dequeue_count, consumer_count
#
# The broker counts from its start. The analysis uses the differences between
# two rows, so the absolute values do not matter.
#
# Credentials: BROKER_USER and BROKER_PASSWORD in the environment, or in the
# secrets file (-s). They are not on the command line, so that they do not show
# in the process list.

set -uo pipefail

host="activemq"
port=8161
queue="cds-profile-unification"
interval=10
out=""
secrets_file=""

usage() {
    cat <<EOF
Usage: $0 -o <output csv> [-H <host>] [-P <port>] [-q <queue>] [-i <seconds>] [-s <secrets file>]

  -o  The output file. The script appends to it.
  -H  The ActiveMQ host. Default: $host.
  -P  The web console port. Default: $port.
  -q  The queue name. Default: $queue.
  -i  The interval in seconds. Default: $interval.
  -s  A file with BROKER_USER and BROKER_PASSWORD lines.

Stop the monitor with kill. It stops also when the output directory is removed.
EOF
}

while getopts "o:H:P:q:i:s:h" opt; do
    case "$opt" in
        o) out="$OPTARG" ;;
        H) host="$OPTARG" ;;
        P) port="$OPTARG" ;;
        q) queue="$OPTARG" ;;
        i) interval="$OPTARG" ;;
        s) secrets_file="$OPTARG" ;;
        h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done
[ -n "$out" ] || { usage >&2; exit 2; }
if [ -n "$secrets_file" ]; then
    BROKER_USER="${BROKER_USER:-$(grep -E '^BROKER_USER=' "$secrets_file" | tail -1 | cut -d= -f2-)}"
    BROKER_PASSWORD="${BROKER_PASSWORD:-$(grep -E '^BROKER_PASSWORD=' "$secrets_file" | tail -1 | cut -d= -f2-)}"
fi
: "${BROKER_USER:?Set BROKER_USER, or use -s}" "${BROKER_PASSWORD:?Set BROKER_PASSWORD, or use -s}"

mbean="org.apache.activemq:type=Broker,brokerName=localhost,destinationType=Queue,destinationName=$queue"
url="http://$host:$port/api/jolokia/read/$mbean/QueueSize,EnqueueCount,DequeueCount,ConsumerCount"

# The credentials go to curl in a config file, not on the command line.
curl_cfg="$(mktemp)"
chmod 600 "$curl_cfg"
trap 'rm -f "$curl_cfg"' EXIT
printf 'user = "%s:%s"\n' "$BROKER_USER" "$BROKER_PASSWORD" >"$curl_cfg"

[ -s "$out" ] || echo "epoch_seconds,queue_size,enqueue_count,dequeue_count,consumer_count" >"$out"
while [ -d "$(dirname "$out")" ]; do
    now="$(date +%s)"
    # Jolokia refuses a request without an Origin header (strict CORS checking).
    line="$(curl -s --max-time 5 -K "$curl_cfg" -H 'Origin: http://localhost' "$url" \
        | python3 -c '
import json, sys
try:
    v = json.load(sys.stdin)["value"]
    print("%d,%d,%d,%d" % (v["QueueSize"], v["EnqueueCount"], v["DequeueCount"], v["ConsumerCount"]))
except Exception:
    print(",,,")
')"
    echo "$now,$line" >>"$out"
    sleep "$interval"
done
