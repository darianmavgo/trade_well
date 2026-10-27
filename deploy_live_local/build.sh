#!/usr/bin/env bash
# build.sh: vendor when needed, then build the way App Engine does.
#
#   build.sh                        vendor if stale, then GOFLAGS=-mod=vendor go build
#   build.sh --changed-since STAMP  exit 0 if a locally replaced module (../schwaber,
#                                   ../backtestgosqlite) has a .go/.sql/go.mod/go.sum
#                                   newer than STAMP, else 1. Used by the file watcher.
#
# trade_orchestrator builds against vendor/ copies of the sibling repos (go.mod
# `replace` directives). Without `go mod vendor` after a change there, the build
# silently uses stale copies. Vendoring runs when vendor/ is missing, or go.mod /
# go.sum, or any file of a locally replaced module is newer than vendor/modules.txt
# (go mod vendor rewrites it on every run).
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"

# Directories of `replace ... => ../path` lines (local modules only).
local_replaces() {
  awk '$1=="replace" && /=>/ { print $NF }' go.mod | while read -r d; do
    case "$d" in ../*|./*|/*) [ -d "$d" ] && echo "$d" ;; esac
  done
}

# Prints the first changed file of a local module newer than $1, if any.
changed_since() {
  local stamp="$1" d hit
  for d in $(local_replaces); do
    hit="$(find "$d" \( -name vendor -o -name .git \) -prune -o \
      \( \( -name '*.go' -o -name '*.sql' -o -name go.mod -o -name go.sum \) -a -newer "$stamp" \) -print 2>/dev/null | head -n 1 || true)"
    if [ -n "$hit" ]; then echo "$hit"; return 0; fi
  done
  return 1
}

if [ "${1:-}" = "--changed-since" ]; then
  [ -n "${2:-}" ] && [ -e "$2" ] || { echo "--changed-since needs an existing stamp file" >&2; exit 2; }
  changed_since "$2" >/dev/null
  exit $?
fi

STAMP="vendor/modules.txt"
why=""
if [ ! -f "$STAMP" ]; then
  why="vendor/ is missing"
elif [ go.mod -nt "$STAMP" ] || [ go.sum -nt "$STAMP" ]; then
  why="go.mod or go.sum changed"
elif hit="$(changed_since "$STAMP")"; then
  why="sibling changed: $hit"
fi

start=$(date +%s)
if [ -n "$why" ]; then
  echo "[build] go mod vendor ($why)"
  go mod vendor
else
  echo "[build] vendor/ is current"
fi
mkdir -p bin
echo "[build] GOFLAGS=-mod=vendor go build -o bin/orchestrator ./cmd/orchestrator"
GOFLAGS=-mod=vendor go build -o bin/orchestrator ./cmd/orchestrator
echo "[build] ok ($(( $(date +%s) - start ))s)"
