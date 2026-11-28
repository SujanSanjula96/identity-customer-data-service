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
# Prints a Markdown summary of the built release artifacts.
#
# A release publishes checksum files beside every zip, which is what an
# installation verifies against. This is for the person watching the build:
# one table, in the run summary, saying what came out and what its digest is —
# so comparing a published artifact against the run that produced it does not
# mean downloading anything.
#
#   artifact-summary.sh <dist-dir> >> "$GITHUB_STEP_SUMMARY"

set -euo pipefail

DIST_DIR="${1:?usage: artifact-summary.sh <dist-dir>}"
[ -d "$DIST_DIR" ] || { echo "artifact-summary: $DIST_DIR not found" >&2; exit 1; }

digest() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# Rounded, in the unit that suits the file: the useful question of a pack's
# size is whether it moved, not its exact byte count, which the checksum
# settles anyway. A metadata file reported as "0 MiB" answers nothing.
size_of() {
  local bytes
  bytes="$(wc -c < "$1" | tr -d '[:space:]')"
  if [ "$bytes" -ge 1048576 ]; then
    echo "$((bytes / 1048576)) MiB"
  else
    echo "$(((bytes + 1023) / 1024)) KiB"
  fi
}

echo "### Release artifacts"
echo
echo "| Artifact | Size | SHA-256 |"
echo "|---|---|---|"

found=0
for zip in "$DIST_DIR"/*.zip; do
  [ -e "$zip" ] || continue
  printf '| `%s` | %s | `%s` |\n' "$(basename "$zip")" "$(size_of "$zip")" "$(digest "$zip")"
  found=$((found + 1))
done

for metadata in "$DIST_DIR"/*-release-metadata.yaml; do
  [ -e "$metadata" ] || continue
  printf '| `%s` | %s | `%s` |\n' "$(basename "$metadata")" "$(size_of "$metadata")" "$(digest "$metadata")"
done

if [ "$found" -eq 0 ]; then
  echo "artifact-summary: no packs in $DIST_DIR" >&2
  exit 1
fi

echo
echo "Each zip is published with a \`.sha256\` and \`.md5\` beside it."
