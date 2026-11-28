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
# Checks that every built pack carries what an installation needs.
#
# A pack that quietly lost a file still builds, still zips and still uploads;
# the loss only shows up when a customer extracts it. This runs on every pull
# request so that a change to the packaging is caught where it was made.
#
#   check-pack.sh <dist-dir>

set -euo pipefail

DIST_DIR="${1:?usage: check-pack.sh <dist-dir>}"
[ -d "$DIST_DIR" ] || { echo "check-pack: $DIST_DIR not found" >&2; exit 1; }

# Paths every pack must contain, relative to the pack root, beside the
# executable and its launcher, which depend on the platform. The executable is
# the product; the rest is what it reads on first start, plus the identity the
# update service resolves the pack by.
REQUIRED="
repository/conf/deployment.yaml
repository/conf/secrets.env
repository/dbscripts/postgres.sql
repository/dbscripts/sqlite.sql
repository/resources/security/README.md
repository/resources/security/server.crt
repository/resources/security/server.key
repository/resources/security/trust_store.pem
repository/resources/security/client_trust_store.pem
updates/product.txt
version.txt
LICENSE.txt
README.md
"

failures=0
packs=0

for zip in "$DIST_DIR"/*.zip; do
  [ -e "$zip" ] || continue
  pack="$(basename "$zip" .zip)"
  packs=$((packs + 1))
  listing="$(unzip -Z1 "$zip")"

  case "$pack" in
    *-windows-*) entry="bin/cdd.exe bin/start.bat" ;;
    *)           entry="bin/cdd bin/start.sh" ;;
  esac

  for required in $entry $REQUIRED; do
    if ! printf '%s\n' "$listing" | grep -qx "$pack/$required"; then
      echo "  x $pack is missing $required" >&2
      failures=$((failures + 1))
    fi
  done

  # A pack is extracted onto a machine where nothing else is installed, so a
  # datasource pointing at a database that is not there means it cannot start
  # at all. pack-config.sh sets this; assert it survived.
  if ! unzip -p "$zip" "$pack/repository/conf/deployment.yaml" 2>/dev/null \
      | grep -qE '^[[:space:]]+type:[[:space:]]*"sqlite"'; then
    echo "  x $pack does not default to the inbuilt database" >&2
    failures=$((failures + 1))
  fi

  # secrets.env is shipped for the operator to fill in. A value in it would be
  # the same secret in every download of the release.
  if unzip -p "$zip" "$pack/repository/conf/secrets.env" 2>/dev/null \
      | grep -qE '^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*[[:space:]]*=[[:space:]]*[^[:space:]]'; then
    echo "  x $pack ships a value in repository/conf/secrets.env" >&2
    failures=$((failures + 1))
  fi

  # The pack README links the documentation of its own release.
  if unzip -p "$zip" "$pack/README.md" 2>/dev/null | grep -q '@VERSION@'; then
    echo "  x $pack has an unfilled @VERSION@ in README.md" >&2
    failures=$((failures + 1))
  fi

  # The product identity has to name the pack it sits in, or the update
  # service resolves updates for a different platform.
  identity="$(unzip -p "$zip" "$pack/updates/product.txt" 2>/dev/null | tr -d '[:space:]')"
  if [ "$identity" != "$pack" ]; then
    echo "  x $pack carries the product identity '$identity'" >&2
    failures=$((failures + 1))
  fi

  [ "$failures" -eq 0 ] && echo "  + $pack"
done

if [ "$packs" -eq 0 ]; then
  echo "check-pack: no packs in $DIST_DIR" >&2
  exit 1
fi
if [ "$failures" -gt 0 ]; then
  echo "check-pack: $failures problem(s) across $packs pack(s)" >&2
  exit 1
fi
echo "check-pack: $packs pack(s) carry every required file."
