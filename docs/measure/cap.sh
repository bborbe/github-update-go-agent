#!/bin/bash
# Operator-side capture: needs the kubectlsun wrapper; writes <dir>/<pod>.log.
# Follows every running update-go pod's log for 90 min, one follower per pod.
# Usage: cap.sh [dir]   (default /tmp/sunlogs)
set -u
dir="${1:-/tmp/sunlogs}"
mkdir -p "$dir"
trap 'kill $(jobs -p) 2>/dev/null' EXIT
end=$((SECONDS + 5400))
while [ "$SECONDS" -lt "$end" ]; do
  for p in $(kubectlsun -n a2a get pods --no-headers 2>/dev/null | awk '$1~/update-go/ && ($3=="Running"){print $1}'); do
    [ -e "$dir/$p.pid" ] && continue
    (kubectlsun -n a2a logs -f "$p" --timestamps > "$dir/$p.log" 2>&1) &
    echo $! > "$dir/$p.pid"
  done
  sleep 10
done
