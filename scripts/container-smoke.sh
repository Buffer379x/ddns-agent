#!/bin/sh
set -eu
image=${1:-ddns-agent:test}
name=ddns-agent-smoke-$$
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
# Non-default port also exercises the configured-port health check.
docker run -d --name "$name" -e DDNS_PORT=9090 -e DDNS_UPDATE_INTERVAL=168h "$image" >/dev/null
ready=false
for attempt in $(seq 1 40); do
  if docker exec "$name" /ddns-agent --health >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
[ "$ready" = true ] || { docker logs "$name"; exit 1; }
uid=$(docker exec "$name" awk '/^Uid:/{print $2}' /proc/1/status)
[ "$uid" != 0 ]
docker exec "$name" test -s /data/bootstrap-password
docker exec "$name" test -s /data/.key
docker stop --time 15 "$name" >/dev/null
[ "$(docker inspect -f '{{.State.ExitCode}}' "$name")" = 0 ]
echo "Container health, custom port, non-root PID 1, bootstrap and graceful shutdown passed."
