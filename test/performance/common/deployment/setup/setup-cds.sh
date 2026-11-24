#!/usr/bin/env bash
# Installs and starts one CDS node. Runs on each CDS node. start-performance.sh
# copies the bundle to ~/cds-perf and runs this script.
#
# Bundle layout (~/cds-perf):
#   cds                                 the CDS binary (linux/amd64): a build of
#                                       cmd/server, or bin/cdd of a CDS release pack
#   cds.env                             the environment of CDS, with the secrets (mode 600)
#   certs/ca.crt, cds.crt, cds.key      from the test CA
#   base-deployment.yaml                config/repository/conf/deployment.yaml of the CDS build,
#                                       or repository/conf/deployment.yaml of the pack
#   setup/resources/cds-deployment.yaml the CDS configuration template
#
# Options:
#   -s <subcommand>   The subcommand that starts the server. A pack binary
#                     (bin/cdd) needs "start". A build of cmd/server takes none.
#
# CDS expands ${NAME} in deployment.yaml from its environment. systemd reads the
# environment from /etc/cds/cds.env, which only root can read. Thus no secret
# is in deployment.yaml.

set -euo pipefail

bundle_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cds_home="/opt/cds"
port=8900
start_cmd=""

while getopts "s:" opt; do
    case "$opt" in
        s) start_cmd="$OPTARG" ;;
        *) echo "Usage: $0 [-s <subcommand>]" >&2; exit 2 ;;
    esac
done
case "$start_cmd" in "" | start) ;; *) echo "Unknown subcommand: $start_cmd" >&2; exit 2 ;; esac

sudo systemctl stop cds 2>/dev/null || true

echo "==> Install CDS to $cds_home"
sudo useradd --system --home "$cds_home" --shell /usr/sbin/nologin cds 2>/dev/null || true
sudo mkdir -p "$cds_home/repository/conf" "$cds_home/certs" /etc/cds
sudo install -m 755 "$bundle_dir/cds" "$cds_home/cds"

# The template is a full configuration. required_scopes comes from the
# configuration of the same CDS build, so that the scope mapping cannot drift.
# The awk block is the same as yaml_block in scripts/local-setup/script.sh.
{
    cat "$bundle_dir/setup/resources/cds-deployment.yaml"
    awk -v key="  required_scopes:" '
        $0 == key             { inside = 1; print; next }
        inside && /^[^ ]/     { inside = 0 }
        inside && /^  [^ ]/   { inside = 0 }
        inside                { print }
    ' "$bundle_dir/base-deployment.yaml"
} | if grep -q '^CDS_SYS_CLIENT_ID=' "$bundle_dir/cds.env"; then cat; else
    # No configured client: CDS calls IS and introspects the tokens with the
    # service credentials that IS sends for each organization. See
    # start-performance.sh.
    grep -v -E '^  (introspection_)?client_(id|secret):'
fi | sudo tee "$cds_home/repository/conf/deployment.yaml" >/dev/null
sudo grep -q 'required_scopes:' "$cds_home/repository/conf/deployment.yaml" \
    || { echo "No required_scopes block in base-deployment.yaml" >&2; exit 1; }

# nginx and IS connect with the name cdslb or cdsN. The certificate has all
# these names.
sudo install -m 644 "$bundle_dir/certs/cds.crt" "$cds_home/certs/server.crt"
sudo install -m 600 "$bundle_dir/certs/cds.key" "$cds_home/certs/server.key"
sudo install -m 644 "$bundle_dir/certs/ca.crt" "$cds_home/certs/trust_store.pem"
sudo install -m 600 "$bundle_dir/cds.env" /etc/cds/cds.env
sudo chown -R cds:cds "$cds_home"

sudo tee /etc/systemd/system/cds.service >/dev/null <<EOF
[Unit]
Description=WSO2 Customer Data Service
After=network-online.target
Wants=network-online.target

[Service]
User=cds
WorkingDirectory=$cds_home
EnvironmentFile=/etc/cds/cds.env
# A pack binary finds its home from CDD_HOME. An older build uses CDS_HOME.
Environment=CDS_HOME=$cds_home
Environment=CDD_HOME=$cds_home
ExecStart=$cds_home/cds${start_cmd:+ $start_cmd}
LimitNOFILE=65535
Restart=on-failure
RestartSec=5
TimeoutStopSec=40
StandardOutput=append:/var/log/cds.log
StandardError=append:/var/log/cds.log

[Install]
WantedBy=multi-user.target
EOF
sudo touch /var/log/cds.log
sudo chown cds:cds /var/log/cds.log
sudo systemctl daemon-reload
sudo systemctl enable cds >/dev/null 2>&1
sudo systemctl start cds

echo "==> Wait for CDS"
check() { curl -s --cacert "$bundle_dir/certs/ca.crt" -o /dev/null -w '%{http_code}' "https://localhost:$port/cds/api/v1/$1"; }
for _ in $(seq 1 60); do
    [ "$(check ready)" = "200" ] && break
    sleep 2
done
if [ "$(check ready)" != "200" ]; then
    echo "CDS is not ready. Last log lines:" >&2
    sudo tail -30 /var/log/cds.log >&2
    exit 1
fi
echo "CDS is up on port $port. Its database and broker are reachable."
