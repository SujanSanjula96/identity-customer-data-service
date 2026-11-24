#!/usr/bin/env bash
# Removes the block that setup-is.sh adds to allow Basic authentication for
# provision-is.sh, and restarts IS. Then IS blocks Basic authentication on its
# APIs again, as the product default of a newer IS does. Runs on each IS node,
# after provision-is.sh provision. Does nothing when deployment.toml has no
# such block (an IS without the compatibility setting).

set -euo pipefail

carbon_home="$HOME/wso2is"
toml="$carbon_home/repository/conf/deployment.toml"
begin="# BEGIN cds-performance setup: Basic authentication for provision-is.sh"
end="# END cds-performance setup: Basic authentication for provision-is.sh"

if ! grep -qxF "$begin" "$toml"; then
    echo "No setup block for Basic authentication. IS is not changed."
    exit 0
fi

echo "==> Remove the setup block for Basic authentication"
awk -v b="$begin" -v e="$end" '$0 == b { skip = 1; next } $0 == e { skip = 0; next } !skip' "$toml" >"$toml.tmp"
grep -q 'compatibility_setting.basic_auth' "$toml.tmp" && { echo "The block is still in $toml" >&2; exit 1; }
cat "$toml.tmp" >"$toml"
rm -f "$toml.tmp"

echo "==> Restart IS"
sudo systemctl restart wso2is
sleep 10
for _ in $(seq 1 120); do
    if [ "$(curl -sk -o /dev/null -w '%{http_code}' https://localhost:9443/api/health-check/v1.0/health)" = "200" ]; then
        break
    fi
    sleep 5
done
[ "$(curl -sk -o /dev/null -w '%{http_code}' https://localhost:9443/api/health-check/v1.0/health)" = "200" ] \
    || { echo "IS did not start. See $carbon_home/repository/logs/wso2carbon.log" >&2; exit 1; }

# shellcheck source=/dev/null
source "$HOME/cds-perf/secrets.env"
# The health check answers before IS loads the compatibility settings. In that
# time Basic authentication can still work, so wait for the 401.
code=""
admin_user="admin:${IS_ADMIN_PASS:?}"
for _ in $(seq 1 24); do
    code="$(curl -sk -o /dev/null -w '%{http_code}' --user "$admin_user" \
        'https://localhost:9443/api/server/v1/applications?limit=1')"
    [ "$code" = "401" ] && break
    sleep 5
done
[ "$code" = "401" ] || { echo "Basic authentication still works on the IS API (HTTP $code)." >&2; exit 1; }
echo "IS blocks Basic authentication on its APIs (HTTP 401)."
