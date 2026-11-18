#!/usr/bin/env bash
# -------------------------------------------------------------------------------------
#
# Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied. See the License for the
# specific language governing permissions and limitations
# under the License.
#
# --------------------------------------------------------------------------------------

# Turns the inputs of the TestGrid workflow into the job matrix.
#
#   DATABASES    comma- or space-separated database names, or "all" (the default)
#   TEST_FILTER  optional test-name pattern, passed to the Make target as test=...
#
# It writes `matrix` and `test` to $GITHUB_OUTPUT (standard output when that is unset,
# so the script can be run by hand) and fails with the valid names when an input is
# not one it knows.

set -euo pipefail

# One line per database: its name and the Make target that runs its integration
# suite. A database added here, and to the description of the `databases` input in
# testgrid.yml, becomes an option of TestGrid.
TABLE='postgres integration-test
sqlite integration-test-sqlite
mysql integration-test-mysql
mssql integration-test-mssql'

OUTPUT="${GITHUB_OUTPUT:-/dev/stdout}"
SUMMARY="${GITHUB_STEP_SUMMARY:-/dev/null}"

all_names() { awk '{print $1}' <<<"$TABLE"; }
valid_names() { all_names | paste -sd, - | sed 's/,/, /g'; }

target_of() { awk -v name="$1" '$1 == name {print $2}' <<<"$TABLE"; }

requested="$(printf '%s' "${DATABASES:-all}" | tr '[:upper:]' '[:lower:]' | tr ',' ' ')"
if [ -z "$(printf '%s' "$requested" | tr -d '[:space:]')" ]; then
  requested="all"
fi

selected=()
add_once() {
  local name
  for name in "${selected[@]+"${selected[@]}"}"; do
    [ "$name" = "$1" ] && return 0
  done
  selected+=("$1")
}

for name in $requested; do
  if [ "$name" = "all" ]; then
    while read -r each; do add_once "$each"; done < <(all_names)
    continue
  fi
  if [ -z "$(target_of "$name")" ]; then
    echo "::error title=Unknown database::'$name' is not a database TestGrid runs. Choose from: $(valid_names), or all."
    exit 1
  fi
  add_once "$name"
done

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
    '. + [{db: $db, target: $target}]' <<<"$include")"
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
