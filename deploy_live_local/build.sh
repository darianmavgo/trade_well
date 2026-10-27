#!/usr/bin/env bash
# build.sh: build the orchestrator the way App Engine does.
#
#   build.sh        go build -o bin/orchestrator ./cmd/orchestrator
#
# The module is self-contained: every dependency is a public Go module, fetched into the module
# cache on first use, so there is no vendor/ to keep fresh and no sibling checkout to watch.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"

start=$(date +%s)
mkdir -p bin
echo "[build] go build -o bin/orchestrator ./cmd/orchestrator"
go build -o bin/orchestrator ./cmd/orchestrator
echo "[build] ok ($(( $(date +%s) - start ))s)"
