#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"

mkdir -p demo
demo_tmp=$(mktemp "${TMPDIR:-/tmp}/minecraft-log-analyzer-demo.XXXXXX.db")
trap 'rm -f "$demo_tmp" "$demo_tmp-shm" "$demo_tmp-wal"' EXIT HUP INT TERM

go run ./cmd/generate-demo -out "$demo_tmp"
mv "$demo_tmp" demo/minecraft-logs-demo.db
