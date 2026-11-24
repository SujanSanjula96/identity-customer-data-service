#!/usr/bin/env bash
# Common functions for the CDS performance test scripts that run on the
# operator machine. The structure follows
# https://github.com/wso2/performance-is (common/common-functions.sh).
#
# These functions must work with bash 3.2 (the default bash of macOS): no
# associative arrays, no mapfile, and no negative array indexes.
#
# The caller sets run_dir. write_ssh_config makes $run_dir/ssh_config, and all
# SSH and SCP calls use it. The private nodes are reached through the bastion
# node with ProxyJump, so the private key never leaves the operator machine.

log() { printf '%s %s\n' "$(date '+%H:%M:%S')" "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

check_command() {
    command -v "$1" >/dev/null 2>&1 || die "Please install $1."
}

format_time() {
    local duration="$1"
    printf '%dh %02dm %02ds\n' $((duration / 3600)) $((duration % 3600 / 60)) $((duration % 60))
}

measure_time() {
    echo $(($(date +%s) - $1))
}

# write_ssh_config KEY_FILE BASTION_IP - writes $run_dir/ssh_config.
# The nodes are new for each run, so the host keys go to a known_hosts file in
# the run directory and not to the known_hosts file of the operator.
write_ssh_config() {
    : "${run_dir:?run_dir is not set}"
    cat >"$run_dir/ssh_config" <<EOF
Host *
    User ubuntu
    IdentityFile $1
    IdentitiesOnly yes
    StrictHostKeyChecking accept-new
    UserKnownHostsFile $run_dir/known_hosts
    ServerAliveInterval 60
    ServerAliveCountMax 10
    ConnectTimeout 20
    LogLevel ERROR

Host bastion
    HostName $2

Host 10.0.*
    ProxyJump bastion
EOF
    chmod 600 "$run_dir/ssh_config"
}

# ssh_node HOST COMMAND... - runs a command on a node. HOST is "bastion" or a
# private IP.
ssh_node() {
    local host="$1"
    shift
    ssh -F "$run_dir/ssh_config" "$host" "$@"
}

# scp_to HOST DEST SOURCE... - copies local files to a node.
scp_to() {
    local host="$1" dest="$2"
    shift 2
    scp -q -r -F "$run_dir/ssh_config" "$@" "$host:$dest"
}

# scp_from HOST SOURCE DEST - copies a file or a directory from a node.
scp_from() {
    scp -q -r -F "$run_dir/ssh_config" "$1:$2" "$3"
}

# wait_for_node HOST - waits until cloud-init has finished on a node.
wait_for_node() {
    local host="$1" tries=0
    until ssh_node "$host" 'cloud-init status --wait >/dev/null 2>&1; test -f /var/lib/cloud/instance/boot-finished' 2>/dev/null; do
        tries=$((tries + 1))
        [ "$tries" -lt 40 ] || die "Node $host is not ready after 20 minutes."
        log "Node $host is not reachable or not ready yet (try $tries of 40). Wait 30 seconds."
        sleep 30
    done
    # The startup script installs the packages. A download can fail, for example
    # when apt tries only the IPv6 addresses of the mirror (the subnet has no
    # IPv6 route). Then run the script again with IPv4 and retries.
    if ssh_node "$host" 'cloud-init status 2>/dev/null | grep -q "status: error"'; then
        log "The startup script failed on $host. Run it again (IPv4, 5 retries)."
        # shellcheck disable=SC2016  # The node expands $s.
        ssh_node "$host" 'printf "Acquire::ForceIPv4 \"true\";\nAcquire::Retries \"5\";\n" |
                sudo tee /etc/apt/apt.conf.d/99cds-perf >/dev/null &&
            for s in /var/lib/cloud/instance/scripts/*; do sudo "$s" >/tmp/startup-again.log 2>&1 || exit 1; done' ||
            die "The startup script failed again on $host. See /tmp/startup-again.log on the node."
    fi
}

# stack_output STACK KEY - one output value of a CloudFormation stack.
stack_output() {
    aws cloudformation describe-stacks --stack-name "$1" \
        --query "Stacks[0].Outputs[?OutputKey=='$2'].OutputValue" --output text
}

# delete_stack STACK - deletes the stack and waits.
delete_stack() {
    local stack="$1" start
    start=$(date +%s)
    log "Delete the stack $stack"
    aws cloudformation delete-stack --stack-name "$stack"
    if aws cloudformation wait stack-delete-complete --stack-name "$stack"; then
        log "Stack deleted in $(format_time "$(measure_time "$start")")"
    else
        log "The stack deletion did not complete. Check it with:"
        log "  aws cloudformation describe-stacks --stack-name $stack"
    fi
}
