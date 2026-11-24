#!/usr/bin/env bash
# Configures nginx as the load balancer in front of the IS nodes and the CDS
# nodes. Runs on the nginx node. Follows common/deployment/setup/setup-nginx.sh
# of performance-is.
#
#   setup-nginx.sh -s is -n <1|2>     https://wso2is:9443 to the IS nodes is1, is2
#   setup-nginx.sh -s cds -n <1|2|3>  https://cdslb:443 to the CDS nodes cds1 to cds3
#
# Run "-s is" after IS starts and before the IS provisioning. Run "-s cds" after
# CDS starts. Each part writes its own file in /etc/nginx/conf.d, so the second
# run keeps the first part.
#
# nginx ends TLS and opens TLS connections to the nodes, which it keeps open.
# Thus each request goes to the next node (round robin), also when the client
# keeps one connection open. The one node patterns also use nginx, so that all
# patterns have the same path.

set -euo pipefail

bundle_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target=""
nodes=""

usage() { echo "Usage: $0 -s is -n <1|2> | -s cds -n <1|2|3>"; }

while getopts "s:n:h" opt; do
    case "$opt" in
        s) target="$OPTARG" ;;
        n) nodes="$OPTARG" ;;
        h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done
case "$target:$nodes" in
    is:1 | is:2 | cds:1 | cds:2 | cds:3) ;;
    *) usage >&2; exit 2 ;;
esac

if [ "$target" = "is" ]; then
    name="wso2is" port=9443 backend="is" backend_port=9443 cert="is"
    ready_path="/api/health-check/v1.0/health"
    # IS builds some URLs from the Host header, so the header keeps the port.
    # shellcheck disable=SC2016  # An nginx variable, not a shell variable.
    host_header='$host:$server_port'
else
    name="cdslb" port=443 backend="cds" backend_port=8900 cert="cds"
    ready_path="/cds/api/v1/ready"
    # shellcheck disable=SC2016  # An nginx variable, not a shell variable.
    host_header='$host'
fi

sudo mkdir -p "/etc/nginx/ssl/$target"
sudo install -m 644 "$bundle_dir/certs/$cert.crt" "/etc/nginx/ssl/$target/server.crt"
sudo install -m 600 "$bundle_dir/certs/$cert.key" "/etc/nginx/ssl/$target/server.key"
sudo install -m 644 "$bundle_dir/certs/ca.crt" "/etc/nginx/ssl/$target/ca.crt"

servers=""
for i in $(seq 1 "$nodes"); do
    servers="$servers    server $backend$i:$backend_port max_fails=0;
"
done

sudo tee "/etc/nginx/conf.d/$target.conf" >/dev/null <<EOF
upstream ${target}_nodes {
$servers    keepalive 512;
    keepalive_requests 100000;
    keepalive_timeout 300s;
}

server {
    listen $port ssl backlog=8192;
    server_name $name;
    ssl_certificate /etc/nginx/ssl/$target/server.crt;
    ssl_certificate_key /etc/nginx/ssl/$target/server.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    server_tokens off;
    ssl_session_cache shared:SSL_$target:20m;
    keepalive_requests 100000;
    keepalive_timeout 300s;
    access_log off;

    location / {
        proxy_pass https://${target}_nodes;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host_header;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_ssl_server_name on;
        proxy_ssl_name $name;
        proxy_ssl_verify on;
        proxy_ssl_trusted_certificate /etc/nginx/ssl/$target/ca.crt;
        proxy_ssl_session_reuse on;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }
}
EOF
sudo rm -f /etc/nginx/sites-enabled/default

# Worker limits, as in performance-is.
sudo sed -i 's/^\(\s*\)worker_connections .*/\1worker_connections 65535;/' /etc/nginx/nginx.conf
grep -q '^worker_rlimit_nofile' /etc/nginx/nginx.conf \
    || sudo sed -i '1i worker_rlimit_nofile 65535;' /etc/nginx/nginx.conf
sudo mkdir -p /etc/systemd/system/nginx.service.d
printf '[Service]\nLimitNOFILE=65535\n' | sudo tee /etc/systemd/system/nginx.service.d/override.conf >/dev/null
sudo systemctl daemon-reload

sudo nginx -t
sudo systemctl restart nginx

# Each node must answer through nginx. With round robin, n requests reach all n nodes.
for _ in $(seq 1 "$nodes"); do
    code="$(curl -s --cacert "$bundle_dir/certs/ca.crt" --resolve "$name:$port:127.0.0.1" -o /dev/null \
        -w '%{http_code}' "https://$name:$port$ready_path")"
    [ "$code" = "200" ] || { echo "nginx does not reach all $backend nodes (HTTP $code)." >&2; exit 1; }
done
echo "nginx balances $nodes $backend node(s) on https://$name:$port."
