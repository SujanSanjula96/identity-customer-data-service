#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Deletes a CDS performance test stack. Use it for a stack that
# start-performance.sh kept (-T or -P), or for a stack that stayed after an
# interrupted run. Works with bash 3.2 (macOS).
#
#   stop-performance.sh <stack name>
#   stop-performance.sh -l          lists the CDS performance stacks

set -euo pipefail

common_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$common_dir/common-functions.sh"
check_command aws

case "${1:-}" in
    -l)
        aws cloudformation describe-stacks \
            --query "Stacks[?starts_with(StackName, 'cds-perf-')].[StackName,StackStatus,CreationTime]" \
            --output table
        ;;
    "" | -h)
        echo "Usage: $0 <stack name> | -l"
        ;;
    cds-perf-*)
        delete_stack "$1"
        ;;
    *)
        die "This script deletes only stacks with the name cds-perf-*."
        ;;
esac
