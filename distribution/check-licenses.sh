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
# Checks that LICENSE.txt lists exactly the modules linked into the packs.
#
# LICENSE.txt ships in every pack as its third-party inventory, so a module
# that is linked but not listed is shipped without its license, and one listed
# at a version that is no longer linked states something the pack does not
# contain. A dependency change still builds and still passes its tests either
# way; this runs on every pull request so the inventory is fixed where the
# dependency changed.
#
#   check-licenses.sh <license-file>
#
# PLATFORMS comes from the Makefile, so the binaries checked are the ones the
# packs carry.

set -euo pipefail

LICENSE_FILE="${1:?usage: check-licenses.sh <license-file>}"
[ -f "$LICENSE_FILE" ] || { echo "check-licenses: $LICENSE_FILE not found" >&2; exit 1; }
: "${PLATFORMS:?PLATFORMS is not set}"

linked="$(mktemp)"
listed="$(mktemp)"
trap 'rm -f "$linked" "$listed"' EXIT

# Every module any platform binary links, as path@version. The build
# constraints differ by platform, so each one is listed on its own.
for platform in $PLATFORMS; do
  CGO_ENABLED=0 GOOS="${platform%/*}" GOARCH="${platform#*/}" go list -deps \
    -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}{{end}}{{end}}' ./cmd/cdd
done | grep . | sort -u > "$linked"

awk '$2 == "module" { print $1 }' "$LICENSE_FILE" | sort -u > "$listed"

failures=0
while read -r module; do
  echo "  x $module is linked but not listed in $LICENSE_FILE" >&2
  failures=$((failures + 1))
done < <(comm -23 "$linked" "$listed")
while read -r module; do
  echo "  x $module is listed in $LICENSE_FILE but not linked" >&2
  failures=$((failures + 1))
done < <(comm -13 "$linked" "$listed")

if [ "$failures" -gt 0 ]; then
  echo "check-licenses: $failures difference(s) between $LICENSE_FILE and the linked modules" >&2
  exit 1
fi
echo "check-licenses: $LICENSE_FILE lists the $(wc -l < "$linked" | tr -d ' ') linked modules."
