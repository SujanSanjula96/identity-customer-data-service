#!/usr/bin/env bash
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
# Checks the license header of the source files a change adds or modifies.
# Files the change doesn't touch are not checked.
#
# The header is the same in every file type; only the comment marker differs
# (/* */ in Go, # in scripts and YAML, -- in SQL, rem in batch files). Its
# copyright line carries the year the file was created, and the current year
# once the file is changed in a later year:
#
#   a new file          Copyright (c) 2026, ...
#   a file created in 2025 and changed in 2026
#                       Copyright (c) (2025-2026), ...
#
# Usage:
#   check-license-headers.sh <base-ref>   check the files changed since <base-ref>
#

set -euo pipefail

BASE_REF=${1:?usage: check-license-headers.sh <base-ref>}

cd "$(git rev-parse --show-toplevel)"

if ! git rev-parse --quiet --verify "${BASE_REF}^{commit}" >/dev/null; then
  echo "check-license-headers: $BASE_REF is not a commit here; fetch it first" >&2
  exit 1
fi

COPYRIGHT_RE='^Copyright \(c\) ([0-9]{4}|\(([0-9]{4})-([0-9]{4})\)), Choreo Labs LLC\. \(https://choreolabs\.com\)\.$'
EXPECTED_BODY='
Choreo Labs licenses this file to you under the Apache License,
Version 2.0 (the "License"); you may not use this file except
in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing,
software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
KIND, either express or implied.  See the License for the
specific language governing permissions and limitations
under the License.'
CURRENT_YEAR=$(date -u +%Y)

source_files() {
  git ls-files -- '*.go' '*.sh' '*.bat' '*.yml' '*.yaml' '*.sql' \
    'Dockerfile' '**/Dockerfile' 'Makefile' '**/Makefile' | grep -v '/testdata/' || true
}

# Prints "<status> <path>" for each source file changed since BASE_REF: A for
# added, M for modified (a renamed file counts as modified).
changed_files() {
  git diff --name-status --diff-filter=AMR "$BASE_REF"...HEAD |
    awk -F'\t' '{ print substr($1, 1, 1) "\t" $NF }' |
    awk -F'\t' 'NR == FNR { source[$0] = 1; next } ($2 in source) { print ($1 == "A" ? "A" : "M") "\t" $2 }' \
      <(source_files) -
}

# Prints the first 15 lines of a file's header with the comment markers,
# borders, shebang and "@echo off" removed and leading blank lines skipped.
header_text() {
  awk '
    FNR > 40 { exit }
    { sub(/\r$/, "") }
    FNR == 1 && /^#!/ { next }
    tolower($0) ~ /^@echo off/ { next }
    {
      line = $0
      if (line ~ /^[ \t]*(\/\*|\*\/)[ \t]*$/) line = ""
      else if (tolower(line) ~ /^rem( |$)/) line = substr(line, 5)
      else sub(/^[ \t]*(\* ?|\*$|# ?|-- ?)/, "", line)
      if (line ~ /^-+$/) line = ""
      if (!started && line == "") next
      started = 1
      print line
      if (++count == 15) exit
    }' "$1"
}

failures=0
fail() {
  echo "  x $1: $2" >&2
  failures=$((failures + 1))
}

checked=0
while IFS=$'\t' read -r status file; do
  checked=$((checked + 1))
  text=$(header_text "$file")
  copyright=${text%%$'\n'*}
  if ! [[ $copyright =~ $COPYRIGHT_RE ]]; then
    if [[ $text == *Copyright* ]]; then
      fail "$file" "the header does not start with the Choreo Labs copyright line"
    else
      fail "$file" "there is no license header"
    fi
    continue
  fi
  if [ "${text#*$'\n'}" != "$EXPECTED_BODY" ]; then
    fail "$file" "the license text differs from the Apache header"
    continue
  fi

  if [ -n "${BASH_REMATCH[2]}" ]; then
    first=${BASH_REMATCH[2]} last=${BASH_REMATCH[3]}
  else
    first=${BASH_REMATCH[1]} last=${BASH_REMATCH[1]}
  fi

  if [ "$first" -ge "$last" ] && [ "$first" != "$last" -o -n "${BASH_REMATCH[2]}" ]; then
    if [ "$first" -lt "$CURRENT_YEAR" ]; then suggested="($first-$CURRENT_YEAR)"; else suggested=$CURRENT_YEAR; fi
    fail "$file" "a year range runs from the creation year to a later year: Copyright (c) $suggested"
  elif [ "$status" = A ]; then
    if [ "$first" != "$CURRENT_YEAR" ] || [ "$last" != "$CURRENT_YEAR" ]; then
      fail "$file" "a new file carries the current year: Copyright (c) $CURRENT_YEAR"
    fi
  elif [ "$first" -ge "$CURRENT_YEAR" ]; then
    if [ "$first" != "$CURRENT_YEAR" ] || [ "$last" != "$CURRENT_YEAR" ]; then
      fail "$file" "a file created in $CURRENT_YEAR carries Copyright (c) $CURRENT_YEAR"
    fi
  elif [ "$last" != "$CURRENT_YEAR" ]; then
    fail "$file" "the file was created in $first and changed in $CURRENT_YEAR: Copyright (c) ($first-$CURRENT_YEAR)"
  fi
done < <(changed_files)

if [ "$failures" -gt 0 ]; then
  echo "check-license-headers: $failures of $checked changed file(s) need the license header fixed" >&2
  echo "Copy the header from a file of the same type, such as cmd/cdd/main.go; the text is in $0." >&2
  exit 1
fi
echo "check-license-headers: all $checked changed source file(s) carry the license header."
