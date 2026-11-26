#!/usr/bin/env bash
# Back up the phronesis data volume (pages, SQLite, blobs, audit log, OAuth
# key) to ./phronesis-backup-<timestamp>.tgz. Stops phronesis for a
# consistent SQLite copy, then starts it again.
#
#   deploy/compose/backup.sh                 # project "phronesis"
#   PROJECT=myproj deploy/compose/backup.sh
#
# OUT must be a directory the Docker VM can see (Colima shares $HOME).
#
# Restore: stop phronesis, then
#   docker run --rm -v <project>_phronesis-data:/data -v "$PWD":/b busybox \
#     sh -c 'cd /data && tar xzf /b/<file>.tgz'
# and start phronesis again.
set -euo pipefail
cd "$(dirname "$0")"
CTX="${DOCKER_CONTEXT:-colima}"
PROJECT="${PROJECT:-phronesis}"
OUT="${OUT:-$PWD}"
file="phronesis-backup-$(date +%Y%m%d-%H%M%S).tgz"
docker --context "$CTX" compose -p "$PROJECT" -f compose.yaml stop phronesis
trap 'docker --context "$CTX" compose -p "$PROJECT" -f compose.yaml start phronesis' EXIT
docker --context "$CTX" run --rm -v "${PROJECT}_phronesis-data:/data:ro" -v "$OUT:/backup" busybox \
  tar czf "/backup/$file" -C /data .
echo "backup written: $OUT/$file"
