#!/usr/bin/env bash
# build_all.sh: go build -o ./bin/ ./... in each trade_well repo.
#
# Every main package in a repo is written to that repo's own bin/ (git-ignored).
# Repos build in dependency order (schwaber, backtestgosqlite, trade_orchestrator).
# trade_orchestrator builds against vendor/ copies of the other two, so it runs
# `go mod vendor` first; otherwise it would silently build stale code.
#
# Usage:
#   ./build_all.sh                        build all three
#   ./build_all.sh trade_orchestrator     only the named repos
#   ./build_all.sh -V                     skip `go mod vendor` in trade_orchestrator
#   ./build_all.sh -c                     also run go vet ./... in each repo
#
# A failure in one repo is reported and the rest still build; exit status is
# non-zero if any repo failed.

set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ALL_REPOS=(schwaber backtestgosqlite trade_orchestrator)
VENDOR=1
VET=0
REPOS=()

while [ $# -gt 0 ]; do
  case "$1" in
    -V) VENDOR=0; shift ;;
    -c) VET=1; shift ;;
    -h|--help) sed -n '2,16p' "$0"; exit 0 ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) REPOS+=("$1"); shift ;;
  esac
done
[ ${#REPOS[@]} -eq 0 ] && REPOS=("${ALL_REPOS[@]}")

if [ -t 1 ]; then B=$'\033[1m'; R=$'\033[31m'; G=$'\033[32m'; X=$'\033[0m'; else B=""; R=""; G=""; X=""; fi

command -v go >/dev/null 2>&1 || { echo "go not found in PATH" >&2; exit 1; }

RESULTS=()
FAILED=0

for name in "${REPOS[@]}"; do
  dir="$ROOT/$name"
  echo
  echo "${B}=== $name ===${X}"
  if [ ! -f "$dir/go.mod" ]; then
    echo "${R}no go.mod in $dir${X}"; RESULTS+=("$name|FAILED|no go.mod"); FAILED=1; continue
  fi

  start=$(date +%s)

  if [ "$name" = "trade_orchestrator" ] && [ "$VENDOR" -eq 1 ]; then
    echo "go mod vendor"
    if ! (cd "$dir" && go mod vendor); then
      echo "${R}go mod vendor failed${X}"; RESULTS+=("$name|FAILED|go mod vendor"); FAILED=1; continue
    fi
  fi

  mkdir -p "$dir/bin"
  echo "go build -o ./bin/ ./..."
  if ! (cd "$dir" && go build -o ./bin/ ./...); then
    echo "${R}build failed${X}"; RESULTS+=("$name|FAILED|go build"); FAILED=1; continue
  fi

  if [ "$VET" -eq 1 ]; then
    echo "go vet ./..."
    if ! (cd "$dir" && go vet ./...); then
      echo "${R}vet failed${X}"; RESULTS+=("$name|FAILED|go vet"); FAILED=1; continue
    fi
  fi

  elapsed=$(( $(date +%s) - start ))
  bins="$(cd "$dir/bin" && ls -1 2>/dev/null | tr '\n' ' ')"
  echo "${G}ok${X} (${elapsed}s) bin/: ${bins:-none}"
  RESULTS+=("$name|OK|${elapsed}s, bin/: ${bins:-none}")
done

echo
echo "${B}=== summary ===${X}"
printf "%-20s %-8s %s\n" "repo" "result" "detail"
for line in "${RESULTS[@]}"; do
  IFS='|' read -r n s d <<<"$line"
  printf "%-20s %-8s %s\n" "$n" "$s" "$d"
done
exit "$FAILED"
