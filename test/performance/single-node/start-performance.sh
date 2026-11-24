#!/usr/bin/env bash
# Runs the CDS performance tests for the single node deployment (1 CDS node(s)).
# All options go to common/start-performance.sh. Use -h to see them.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "$script_dir/../common/start-performance.sh" -g 1 "$@"
