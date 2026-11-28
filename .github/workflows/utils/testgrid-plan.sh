#!/usr/bin/env bash
# -------------------------------------------------------------------------------------
#
# Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
#
# Choreo Labs licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.
#
# --------------------------------------------------------------------------------------

# Turns the inputs of the TestGrid workflow into the job matrix.
#
#   DB_<NAME>    one per database, such as DB_POSTGRES or DB_ORACLE23AI: "true" runs
#                it, "false" leaves it out. An unset one runs, as a ticked box does,
#                so the script run by hand with none set plans every database.
#   TEST_FILTER  optional test-name pattern, passed to the Make target as test=...
#
# It writes `matrix` and `test` to $GITHUB_OUTPUT (standard output when that is unset,
# so the script can be run by hand) and fails when no database is ticked or the test
# filter is not a list of test names.

set -euo pipefail

# One line per database: its name, the Make target that runs its integration suite, and
# for a database that has several releases the container image of the release and, for
# Oracle, its service name (both are left out for the others). A database added here, and
# as a tickbox to both input lists in testgrid.yml and to the DB_ variables of its Plan
# step, becomes an option of TestGrid.
TABLE='postgres integration-test
sqlite integration-test-sqlite
mysql integration-test-mysql
mssql2022 integration-test-mssql mcr.microsoft.com/mssql/server:2022-latest
mssql2019 integration-test-mssql mcr.microsoft.com/mssql/server:2019-latest
mssql2017 integration-test-mssql mcr.microsoft.com/mssql/server:2017-latest
oracle21c integration-test-oracle gvenzl/oracle-xe:21-slim XEPDB1
oracle23ai integration-test-oracle gvenzl/oracle-free:23-slim FREEPDB1'

OUTPUT="${GITHUB_OUTPUT:-/dev/stdout}"
SUMMARY="${GITHUB_STEP_SUMMARY:-/dev/null}"

all_names() { awk '{print $1}' <<<"$TABLE"; }
valid_names() { all_names | paste -sd, - | sed 's/,/, /g'; }

target_of() { awk -v name="$1" '$1 == name {print $2}' <<<"$TABLE"; }
image_of() { awk -v name="$1" '$1 == name {print $3}' <<<"$TABLE"; }
service_of() { awk -v name="$1" '$1 == name {print $4}' <<<"$TABLE"; }

selected=()
while read -r name; do
  variable="DB_$(printf '%s' "$name" | tr '[:lower:]' '[:upper:]')"
  if [ "${!variable:-true}" = "true" ]; then
    selected+=("$name")
  fi
done < <(all_names)

if [ "${#selected[@]}" -eq 0 ]; then
  echo "::error title=No database ticked::Tick at least one of: $(valid_names)."
  exit 1
fi

# A test-name pattern goes to `go test -run` through Make, so only the characters of
# test names and alternation are accepted.
filter="${TEST_FILTER:-}"
if [ -n "$filter" ] && ! [[ "$filter" =~ ^[A-Za-z0-9_|]+$ ]]; then
  echo "::error title=Invalid test filter::Use test names separated by |, for example Test_Profile|Test_Consent."
  exit 1
fi

include='[]'
for name in "${selected[@]}"; do
  include="$(jq -c --arg db "$name" --arg target "$(target_of "$name")" \
    --arg image "$(image_of "$name")" --arg service "$(service_of "$name")" \
    '. + [{db: $db, target: $target, image: $image, service: $service}]' <<<"$include")"
done

{
  echo "matrix={\"include\":$include}"
  echo "test=$filter"
} >>"$OUTPUT"

{
  echo "### TestGrid plan"
  echo
  echo "- Databases: ${selected[*]}"
  echo "- Test filter: ${filter:-none}"
} >>"$SUMMARY"
