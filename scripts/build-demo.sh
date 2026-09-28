#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"

go run . \
  -report-only \
  -db demo/minecraft-logs-demo.db \
  -out docs \
  -server-name "Blockhaven Synthetic Demo" \
  -timezone UTC \
  -generated-at 2026-09-17T18:00:00Z

touch docs/.nojekyll
