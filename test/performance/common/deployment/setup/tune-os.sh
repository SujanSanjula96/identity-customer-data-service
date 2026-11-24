#!/usr/bin/env bash
# Raises the OS limits for a load test node: open files, the listen backlog,
# and the local port range. Runs on each node with sudo. The values are the
# same type of change that performance-is makes in setup-nginx.sh.

set -euo pipefail

sudo tee /etc/sysctl.d/90-cds-perf.conf >/dev/null <<'EOF'
fs.file-max = 2097152
net.core.somaxconn = 8192
net.ipv4.tcp_max_syn_backlog = 8192
net.ipv4.ip_local_port_range = 10240 65535
net.ipv4.tcp_tw_reuse = 1
net.ipv4.tcp_fin_timeout = 15
EOF
sudo sysctl -q --system

sudo tee /etc/security/limits.d/90-cds-perf.conf >/dev/null <<'EOF'
*       soft    nofile  65535
*       hard    nofile  65535
EOF
echo "OS limits set."

# The daily package upgrade of Ubuntu (apt-daily-upgrade and
# unattended-upgrades) restarts services, for example wso2is, during a test.
# A test node lives for some hours only, so stop the timers. If an upgrade
# runs now, it completes first.
sudo systemctl disable --now apt-daily.timer apt-daily-upgrade.timer >/dev/null 2>&1 || true
sudo systemctl disable --now unattended-upgrades.service >/dev/null 2>&1 || true
echo "Automatic package upgrades stopped."

# sysstat records the CPU, memory, disk, and network of the node. The default
# interval is 10 minutes, which is too long for a 15 minute test. Use 1 minute.
if systemctl list-unit-files sysstat-collect.timer >/dev/null 2>&1; then
    sudo mkdir -p /etc/systemd/system/sysstat-collect.timer.d
    printf '[Timer]\nOnCalendar=\nOnCalendar=*:*:00\n' \
        | sudo tee /etc/systemd/system/sysstat-collect.timer.d/override.conf >/dev/null
    sudo systemctl daemon-reload
    sudo systemctl restart sysstat-collect.timer || true
    echo "sysstat records each minute."
fi
