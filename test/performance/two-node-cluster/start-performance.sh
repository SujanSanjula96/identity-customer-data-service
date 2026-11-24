#!/usr/bin/env bash
# Runs the CDS performance tests for the two node cluster deployment (2 CDS nodes, 2 IS nodes).
# All options go to common/start-performance.sh. Use -h to see them.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "$script_dir/../common/start-performance.sh" -g 2 -i 2 "$@"
