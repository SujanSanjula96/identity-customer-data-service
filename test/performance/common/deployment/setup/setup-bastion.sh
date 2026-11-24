#!/usr/bin/env bash
# Prepares the bastion node, which is also the JMeter node, as in
# performance-is. Runs on the bastion node. start-performance.sh copies the
# bundle to ~/cds-perf and runs this script.
#
#   - Installs Apache JMeter.
#   - Applies dbscripts/postgres.sql to the CDS database, if the schema is not
#     there. A database from a snapshot already has the schema and the data.
#
# Bundle layout (~/cds-perf):
#   secrets.env          CDS_DB_HOST, CDS_DB_USER, CDS_DB_PASS, IS_ADMIN_PASS, and
#                        CDS_SYNC_USER and CDS_SYNC_PASS for the sync use cases (U3, U8, U12).
#                        IS_DB_HOST, IS_DB_USER, and IS_DB_PASS for the query
#                        statistics of the IS database (optional).
#   dbscripts/postgres.sql
#   perf/common/         test/performance/common (jmeter/ and tools/)

set -euo pipefail

bundle_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
jmeter_version="5.6.3"
cds_db_host=""

while getopts "c:j:h" opt; do
    case "$opt" in
        c) cds_db_host="$OPTARG" ;;
        j) jmeter_version="$OPTARG" ;;
        h) echo "Usage: $0 -c <cds_db_host> [-j <jmeter_version>]"; exit 0 ;;
        *) exit 2 ;;
    esac
done
[ -n "$cds_db_host" ] || { echo "Usage: $0 -c <cds_db_host> [-j <jmeter_version>]" >&2; exit 2; }

# shellcheck source=/dev/null
source "$bundle_dir/secrets.env"
: "${CDS_DB_USER:?}" "${CDS_DB_PASS:?}"

echo "==> Apache JMeter $jmeter_version"
if [ ! -x /opt/jmeter/bin/jmeter ] || ! /opt/jmeter/bin/jmeter --version 2>/dev/null | grep -q "$jmeter_version"; then
    name="apache-jmeter-$jmeter_version"
    url="https://archive.apache.org/dist/jmeter/binaries/$name.tgz"
    work="$(mktemp -d)"
    curl -fsSL -o "$work/$name.tgz" "$url"
    curl -fsSL -o "$work/$name.tgz.sha512" "$url.sha512"
    [ "$(awk '{print $1}' "$work/$name.tgz.sha512")" = "$(sha512sum "$work/$name.tgz" | awk '{print $1}')" ] \
        || { echo "SHA-512 check failed for $name" >&2; exit 1; }
    sudo rm -rf /opt/jmeter
    sudo mkdir -p /opt/jmeter
    sudo tar -xzf "$work/$name.tgz" -C /opt/jmeter --strip-components=1
    sudo ln -sf /opt/jmeter/bin/jmeter /usr/local/bin/jmeter
    rm -rf "$work"
fi
jmeter --version 2>/dev/null | grep -o "$jmeter_version" | head -1

echo "==> CDS database schema on $cds_db_host"
export PGPASSWORD="$CDS_DB_PASS"
psql_cds() { psql -h "$cds_db_host" -U "$CDS_DB_USER" -d cds_db "$@"; }
if [ "$(psql_cds -tAc "SELECT to_regclass('public.profiles') IS NOT NULL")" = "t" ]; then
    echo "The schema exists (for example from a snapshot). It is not changed."
else
    psql_cds -q -v ON_ERROR_STOP=1 -f "$bundle_dir/dbscripts/postgres.sql" >/dev/null
    echo "Applied dbscripts/postgres.sql."
fi
# pg_stat_statements shows the cost of each CDS query after a test.
psql_cds -q -c 'CREATE EXTENSION IF NOT EXISTS pg_stat_statements' >/dev/null 2>&1 \
    || echo "pg_stat_statements is not available. The query statistics are not collected."
psql_cds -tAc "SELECT 'profiles: ' || count(*) FROM profiles"
unset PGPASSWORD

# The IS databases (IDENTITY_DB, UM_DB, and the others) are on one server.
# pg_stat_statements in its "postgres" database shows the statements of all of
# them. IS connects with the same user, so this user can read the query text.
if [ -n "${IS_DB_HOST:-}" ]; then
    echo "==> Query statistics of the IS database on $IS_DB_HOST"
    if PGPASSWORD="${IS_DB_PASS:-}" psql -h "$IS_DB_HOST" -U "${IS_DB_USER:-}" -d postgres -q \
        -c 'CREATE EXTENSION IF NOT EXISTS pg_stat_statements' >/dev/null 2>&1; then
        echo "pg_stat_statements is ready."
    else
        echo "pg_stat_statements is not available. The IS query statistics are not collected."
    fi
fi
