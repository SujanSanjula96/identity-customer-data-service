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
# Writes the release body that goes above GitHub's generated change list.
#
# A release publishes a zip per platform, each with its checksums, and the
# release metadata. Landing on that page, the questions are always the same:
# which one do I want, is it the file you built, and what do I do with it. The generated "What's Changed" list answers none of them, so
# this answers them first — from the artifacts themselves, never from a list
# kept by hand that would drift from what was built.
#
#   release-notes.sh <dist-dir> [version] [--changelog <file>]
#
# The change list is GitHub's, generated from the merged pull requests and
# grouped by .github/release.yml. It is passed in rather than fetched here, so
# this script stays usable from a working copy and on a pull request, where
# there is no release to generate one for.
#
# Called by the release workflow, which passes the result as the release body.

set -euo pipefail

DIST_DIR="${1:?usage: release-notes.sh <dist-dir> [version] [--changelog <file>]}"
shift

VERSION=""
CHANGELOG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --changelog) CHANGELOG="${2:?--changelog requires a file}"; shift 2 ;;
    *)           VERSION="$1"; shift ;;
  esac
done
[ -n "$VERSION" ] || VERSION="$(tr -d '[:space:]' < version.txt)"

PACK_VERSION="${VERSION#v}"
PRODUCT="cdd"
REPO="${CDD_REPO:-wso2/identity-customer-data-service}"

[ -d "$DIST_DIR" ] || { echo "release-notes: $DIST_DIR not found" >&2; exit 1; }

# The checksum files are what an installation verifies against, so the digests
# shown here are read from those rather than recomputed — if the two ever
# disagreed, this would be the wrong place to find out.
digest_of() {
  local file="$1.sha256"
  [ -f "$file" ] || { echo "unavailable"; return; }
  awk '{print $1; exit}' "$file"
}

# --- Introduction --------------------------------------------------------- #
# GitHub already shows the release name above this, so the body opens with
# what the reader cannot see from the title.
cat <<INTRO
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Release](https://img.shields.io/github/v/release/$REPO?color=blue)](https://github.com/$REPO/releases/latest)
[![Last commit](https://img.shields.io/github/last-commit/$REPO.svg)](https://github.com/$REPO/commits/main)

The Customer Data Directory builds unified customer profiles from the
identity and behavioural data an organisation already holds, and serves them
to the applications that personalise on them.

CDD runs as its own process beside an Identity Server. The two are
separate installations with separate lifecycles: CDD is installed and updated
on its own, and the Identity Server carries its own side of the integration.

INTRO

# --- Downloads ------------------------------------------------------------ #
echo "## Downloads"
echo
echo "| Platform | Artifact | SHA-256 |"
echo "|---|---|---|"

found=0
for zip in "$DIST_DIR"/$PRODUCT-*-"$PACK_VERSION".zip; do
  [ -e "$zip" ] || continue
  name="$(basename "$zip")"
  # cdd-<os>-<arch>-<version>.zip
  platform="$(echo "$name" | sed -E "s/^$PRODUCT-([a-z0-9]+)-([a-z0-9]+)-.*/\1\/\2/")"
  printf '| %s | [%s](https://github.com/%s/releases/download/%s/%s) | `%s` |\n' \
    "$platform" "$name" "$REPO" "$VERSION" "$name" "$(digest_of "$zip")"
  found=$((found + 1))
done
[ "$found" -gt 0 ] || { echo "release-notes: no packs in $DIST_DIR" >&2; exit 1; }

echo
echo "Each zip is published with a \`.sha256\` and \`.md5\` beside it:"
echo
echo '```bash'
echo "sha256sum -c $PRODUCT-linux-amd64-$PACK_VERSION.zip.sha256          # Linux"
echo "shasum -a 256 -c $PRODUCT-darwin-arm64-$PACK_VERSION.zip.sha256     # macOS"
echo '```'
echo
echo "On Windows, compare \`Get-FileHash $PRODUCT-windows-amd64-$PACK_VERSION.zip\` with the \`.sha256\` file."
echo

# --- Installing ----------------------------------------------------------- #
cat <<NOTES
## Installing

Extracting the zip is the installation. Before the first start, fill in
\`repository/conf/secrets.env\`, which the pack ships with every value empty:
at least \`SERVICE_CREDENTIALS_ENCRYPTION_KEY\`, generated for this installation
with \`openssl rand -base64 32\`, and \`AUTH_SERVER_ADMIN_PASSWORD\`, the
\`admin_password\` below.

\`\`\`bash
unzip $PRODUCT-linux-amd64-$PACK_VERSION.zip -d /opt
cd /opt/$PRODUCT-linux-amd64-$PACK_VERSION
vi repository/conf/secrets.env
bin/start.sh
\`\`\`

On Windows, extract the zip and run \`bin\\start.bat\`.

The macOS executable is not notarized. If the zip was downloaded with a
browser and extracted in Finder, macOS blocks it as coming from an unidentified
developer. Clear the quarantine flag once, after extracting:

\`\`\`bash
xattr -dr com.apple.quarantine $PRODUCT-darwin-arm64-$PACK_VERSION
\`\`\`

Then switch CDD on in the Identity Server's \`deployment.toml\` and restart it:

\`\`\`toml
[customer_data_service]
enable = true
server_url = "https://localhost:8900"
admin_username = "admin"
admin_password = "<AUTH_SERVER_ADMIN_PASSWORD in secrets.env>"
\`\`\`

See the [installation guide](https://github.com/$REPO/blob/$VERSION/docs/guides/on-prem-installation.md).

The Identity Server's side of the integration — the CDD extension bundles and
the CDD Console — is delivered with the Identity Server rather than with this
release, and is updated through its own stream.
NOTES

# --- What changed ---------------------------------------------------------- #
if [ -n "$CHANGELOG" ] && [ -s "$CHANGELOG" ]; then
  echo
  cat "$CHANGELOG"
fi

# --- Documentation, contributing, reporting ------------------------------ #
cat <<MORE

## Documentation

The documentation for this version is in the [docs](https://github.com/$REPO/tree/$VERSION/docs)
folder of the repository, and the API is described in
[\`api/customer-data-service.yaml\`](https://github.com/$REPO/blob/$VERSION/api/customer-data-service.yaml).
MORE

# --- License --------------------------------------------------------------- #
cat <<'LICENCE'

## License

Licensed under the Apache License, Version 2.0. See
[LICENSE](https://github.com/wso2/identity-customer-data-service/blob/main/LICENSE).
The third-party inventory for the binaries in this release is in `LICENSE.txt`
inside each pack.
LICENCE
