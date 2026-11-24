#!/usr/bin/env bash
# Installs and starts Apache ActiveMQ Classic on the ActiveMQ node.
# Runs on the ActiveMQ node. start-performance.sh copies the bundle to
# ~/cds-perf and runs this script.
#
# CDS uses the STOMP transport (port 61613). The broker uses the KahaDB store
# on the local disk, as a broker of a real deployment does.

set -euo pipefail

bundle_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
activemq_version="5.18.7"
heap="2g"
install_dir="/opt/activemq"

usage() {
    cat <<EOF
Usage: $0 [-v <version>] [-m <heap>]

  -v  ActiveMQ Classic version. Default: $activemq_version.
  -m  Broker heap size. Default: $heap.

Reads BROKER_USER and BROKER_PASSWORD from $bundle_dir/secrets.env.
EOF
}

while getopts "v:m:h" opt; do
    case "$opt" in
        v) activemq_version="$OPTARG" ;;
        m) heap="$OPTARG" ;;
        h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done

# shellcheck source=/dev/null
source "$bundle_dir/secrets.env"
: "${BROKER_USER:?BROKER_USER is not set in secrets.env}"
: "${BROKER_PASSWORD:?BROKER_PASSWORD is not set in secrets.env}"

name="apache-activemq-$activemq_version"
url="https://archive.apache.org/dist/activemq/$activemq_version/$name-bin.tar.gz"

echo "==> Download $name"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
curl -fsSL -o "$work/$name-bin.tar.gz" "$url"
curl -fsSL -o "$work/$name-bin.tar.gz.sha512" "$url.sha512"
expected="$(awk '{print $1}' "$work/$name-bin.tar.gz.sha512")"
actual="$(sha512sum "$work/$name-bin.tar.gz" | awk '{print $1}')"
[ "$expected" = "$actual" ] || { echo "SHA-512 check failed for $name" >&2; exit 1; }

echo "==> Install to $install_dir"
sudo systemctl stop activemq 2>/dev/null || true
sudo rm -rf "$install_dir"
sudo mkdir -p "$install_dir"
sudo tar -xzf "$work/$name-bin.tar.gz" -C "$install_dir" --strip-components=1
sudo useradd --system --home "$install_dir" --shell /usr/sbin/nologin activemq 2>/dev/null || true

# Credentials for the STOMP clients. The broker uses the simple
# authentication plugin with one user.
conf="$install_dir/conf/activemq.xml"
sudo python3 - "$conf" <<'PY'
import os, sys, re
path = sys.argv[1]
s = open(path).read()
plugin = """
        <plugins>
            <simpleAuthenticationPlugin anonymousAccessAllowed="false">
                <users>
                    <authenticationUser username="${broker.user}" password="${broker.password}" groups="users,admins"/>
                </users>
            </simpleAuthenticationPlugin>
        </plugins>
"""
if "simpleAuthenticationPlugin" not in s:
    s = s.replace("</broker>", plugin + "    </broker>", 1)
# Only the STOMP and OpenWire transports. Larger frame size and more
# connections than the default.
s = re.sub(r"<transportConnectors>.*?</transportConnectors>",
           """<transportConnectors>
            <transportConnector name="openwire" uri="tcp://0.0.0.0:61616?maximumConnections=1000&amp;wireFormat.maxFrameSize=104857600"/>
            <transportConnector name="stomp" uri="stomp://0.0.0.0:61613?maximumConnections=1000&amp;wireFormat.maxFrameSize=104857600"/>
        </transportConnectors>""", s, flags=re.S)
open(path, "w").write(s)
PY

# The default activemq.xml loads conf/credentials.properties into the Spring
# placeholders, so the ${broker.user} and ${broker.password} values above come
# from that file. Only the broker user can read it.
grep -q 'credentials.properties' "$conf" \
    || { echo "activemq.xml does not load credentials.properties. Check the version." >&2; exit 1; }
# Replace the file: the default users system/manager and guest/password must
# not stay.
printf 'broker.user=%s\nbroker.password=%s\n' "$BROKER_USER" "$BROKER_PASSWORD" \
    | sudo tee "$install_dir/conf/credentials.properties" >/dev/null
# The JAAS and JMX files of the pack have the default user admin/admin. The
# broker does not use them, but no default user must stay. The broker user
# replaces it.
printf '%s=%s\n' "$BROKER_USER" "$BROKER_PASSWORD" | sudo tee "$install_dir/conf/users.properties" >/dev/null
printf 'admins=%s\n' "$BROKER_USER" | sudo tee "$install_dir/conf/groups.properties" >/dev/null
printf '%s %s\n' "$BROKER_USER" "$BROKER_PASSWORD" | sudo tee "$install_dir/conf/jmx.password" >/dev/null
printf '%s readwrite\n' "$BROKER_USER" | sudo tee "$install_dir/conf/jmx.access" >/dev/null
sudo grep -q "^broker.user=$BROKER_USER\$" "$install_dir/conf/credentials.properties" \
    || { echo "Cannot write broker.user to credentials.properties." >&2; exit 1; }

# bin/env reads ACTIVEMQ_OPTS_MEMORY near the top of the file, so a line at the
# end has no effect. Put the value before that use.
sudo sed -i "1a ACTIVEMQ_OPTS_MEMORY=\"-Xms$heap -Xmx$heap\"" "$install_dir/bin/env"
# The web console (port 8161) gives the queue statistics through Jolokia. The
# queue monitor on the bastion node reads them, so the console listens on all
# addresses, and the broker user replaces the default admin/admin user.
sudo sed -i 's|<property name="host" value="127.0.0.1"/>|<property name="host" value="0.0.0.0"/>|' \
    "$install_dir/conf/jetty.xml"
grep -q '<property name="host" value="0.0.0.0"/>' "$install_dir/conf/jetty.xml" \
    || { echo "Cannot set the web console host in jetty.xml. Check the version." >&2; exit 1; }
printf '%s: %s, admin\n' "$BROKER_USER" "$BROKER_PASSWORD" \
    | sudo tee "$install_dir/conf/jetty-realm.properties" >/dev/null

sudo chown -R activemq:activemq "$install_dir"
sudo chmod 600 "$install_dir/conf/credentials.properties" "$install_dir/conf/jetty-realm.properties" \
    "$install_dir/conf/users.properties" "$install_dir/conf/groups.properties" \
    "$install_dir/conf/jmx.password" "$install_dir/conf/jmx.access"

sudo tee /etc/systemd/system/activemq.service >/dev/null <<EOF
[Unit]
Description=Apache ActiveMQ Classic
After=network.target

[Service]
Type=forking
User=activemq
WorkingDirectory=$install_dir
ExecStart=$install_dir/bin/activemq start
ExecStop=$install_dir/bin/activemq stop
LimitNOFILE=65535
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

echo "==> Start the broker"
sudo systemctl daemon-reload
sudo systemctl enable --now activemq

for _ in $(seq 1 60); do
    if (exec 3<>/dev/tcp/127.0.0.1/61613) 2>/dev/null; then
        echo "ActiveMQ $activemq_version listens on STOMP port 61613."
        pgrep -af 'activemq.jar' | grep -q -- "-Xmx$heap" \
            || echo "WARNING: the broker does not use the heap -Xmx$heap." >&2
        exit 0
    fi
    sleep 2
done
echo "ActiveMQ did not open port 61613. See $install_dir/data/activemq.log" >&2
exit 1
