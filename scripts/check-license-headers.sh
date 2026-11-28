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
# Checks that every tracked source file starts with the Apache license header.
#
# The header is the same in every file type; only the comment marker differs
# (/* */ in Go, # in scripts and YAML, -- in SQL, rem in batch files). The
# copyright line takes the creation year, or a (first-last) range for a file
# that keeps being changed.
#
# Usage:
#   check-license-headers.sh              check every tracked source file
#   check-license-headers.sh <base-ref>   also check that files added since
#                                         <base-ref> carry the current year
#

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

COPYRIGHT_RE='^Copyright \(c\) ([0-9]{4}|\([0-9]{4}-[0-9]{4}\)), Choreo Labs LLC\. \(https://choreolabs\.com\)\.$'
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
BASE_REF=${1:-}

source_files() {
  git ls-files -- '*.go' '*.sh' '*.bat' '*.yml' '*.yaml' '*.sql' \
    'Dockerfile' '**/Dockerfile' 'Makefile' '**/Makefile' | grep -v '/testdata/' || true
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
while IFS= read -r file; do
  checked=$((checked + 1))
  text=$(header_text "$file")
  copyright=${text%%$'\n'*}
  if ! [[ $copyright =~ $COPYRIGHT_RE ]]; then
    fail "$file" "the header does not start with the Choreo Labs copyright line"
    continue
  fi
  if [ "${text#*$'\n'}" != "$EXPECTED_BODY" ]; then
    fail "$file" "the license text differs from the Apache header"
    continue
  fi
  years=${BASH_REMATCH[1]//[()]/}
  first=${years%-*} last=${years#*-}
  if [ "$last" -gt "$CURRENT_YEAR" ] || { [ "$first" != "$last" ] && [ "$first" -ge "$last" ]; }; then
    fail "$file" "the copyright year is in the future or the range does not increase"
  fi
done < <(source_files)

if [ -n "$BASE_REF" ]; then
  while IFS= read -r file; do
    text=$(header_text "$file")
    if [[ ${text%%$'\n'*} =~ \(c\)\ \(?([0-9]{4}) ]] && [ "${BASH_REMATCH[1]}" != "$CURRENT_YEAR" ]; then
      fail "$file" "a new file carries ${BASH_REMATCH[1]}; use the current year, $CURRENT_YEAR"
    fi
  done < <(git diff --name-only --diff-filter=A "$BASE_REF"...HEAD | grep -Fx -f <(source_files) || true)
fi

if [ "$failures" -gt 0 ]; then
  echo "check-license-headers: $failures of $checked file(s) need the license header fixed" >&2
  echo "Copy the header from a file of the same type, such as cmd/cdd/main.go; the text is in $0." >&2
  exit 1
fi
echo "check-license-headers: all $checked source files carry the license header."
