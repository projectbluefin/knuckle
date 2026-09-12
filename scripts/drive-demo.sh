#!/usr/bin/env bash
# Drive the knuckle TUI for demo recording.
#
# Uses --demo mode: hardcoded mock hardware + sysexts, no network, no disk writes.
# Works on any machine — CI, dev laptop, recording rig.
#
# Usage:
#   # Build first if needed:
#   go build -o bin/knuckle ./cmd/knuckle
#
#   # Record with asciinema:
#   asciinema rec -c "./scripts/drive-demo.sh" demo/out.cast
#
#   # Or run VHS (generates GIF directly):
#   vhs demo/knuckle-demo.tape
set -u

BINARY=""
for candidate in "bin/knuckle" "./knuckle"; do
    if [[ -x "$candidate" ]]; then
        BINARY="$candidate"
        break
    fi
done
if [[ -z "$BINARY" ]]; then
    echo "knuckle binary not found — run 'go build -o bin/knuckle ./cmd/knuckle' first" >&2
    exit 1
fi

FIFO=$(mktemp -u)
mkfifo "$FIFO"
trap 'rm -f "$FIFO"' EXIT

# --demo: mock hardware/catalog, implies --dry-run (no real writes)
"$BINARY" --demo < "$FIFO" &
KNUCKLE_PID=$!
exec 3>"$FIFO"

send() { printf "%b" "$1" >&3; }
pause() { sleep "$1"; }

# Demo mode starts instantly (no network fetch delay)
pause 1.5

# Welcome — browse channel cards with arrow keys
send "\033[B"; pause 0.6   # ↓ lts
send "\033[B"; pause 0.6   # ↓ beta
send "\033[B"; pause 0.6   # ↓ alpha
send "\033[A"; pause 0.5   # ↑ beta
send "\033[A"; pause 0.5   # ↑ lts
send "\033[A"; pause 0.8   # ↑ stable

# Confirm stable, advance to Network
send "\r"; pause 1.2

# Network — accept DHCP default (two groups)
send "\r"; pause 0.8
send "\r"; pause 1.2

# Storage — select first disk
send "\r"; pause 1.2

# User form — identity group, then auth group
send "\r"; pause 0.8
send "\r"; pause 1.2

# Sysext — browse and toggle three extensions
send "\033[B"; pause 0.4   # ↓ to docker
send " ";      pause 0.5   # toggle on
send "\033[B"; pause 0.4   # ↓ containerd
send "\033[B"; pause 0.4   # ↓ kubernetes
send " ";      pause 0.5   # toggle on
send "\033[B"; pause 0.4   # ↓ tailscale
send " ";      pause 0.6   # toggle on
send "\r";     pause 1.2   # advance

# Update strategy — default reboot
send "\r"; pause 1.2

# Review — pause to let the summary render
pause 2.5

# Quit without confirming install
send "q"; pause 0.5
send "q"; pause 1.0

exec 3>&-
rm -f "$FIFO"
wait "$KNUCKLE_PID" 2>/dev/null
exit 0
