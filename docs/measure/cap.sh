#!/bin/bash
# follow every update-go pod's log until 90 min elapse
end=$((SECONDS+5400))
while [ $SECONDS -lt $end ]; do
  for p in $(kubectlsun -n a2a get pods --no-headers 2>/dev/null | awk '$1~/update-go/ && ($3=="Running"){print $1}'); do
    [ -e /tmp/sunlogs/$p.pid ] && continue
    (kubectlsun -n a2a logs -f $p --timestamps > /tmp/sunlogs/$p.log 2>&1) & echo $! > /tmp/sunlogs/$p.pid
  done
  sleep 10
done
