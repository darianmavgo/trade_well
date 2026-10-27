#!/usr/bin/env bash
# rgs.sh: recursive git status. Read-only.
#
# Walks ROOT for every git repo (including nested ones such as data/.git and repos
# whose directory the parent ignores) and prints, per repo, the branch, ahead/behind
# and every changed or untracked file. With --since it also lists files modified
# recently that git cannot show: gitignored files (databases, binaries) and files
# outside any repo (scratch dirs, memory).
#
# Usage:
#   rgs.sh [-r ROOT] [-s SINCE] [-x EXTRA_DIR]... [-d DEPTH]
#     -r ROOT   directory to search (default: the parent of this repo, ../ )
#     -s SINCE  fd time filter: a duration (30min, 2h, 1d) or a date (2026-10-05)
#     -x DIR    extra non-repo directory to scan for changes since SINCE (repeatable)
#     -d DEPTH  how deep to look for .git (default 4)

set -u
ROOT=""
SINCE=""
DEPTH=4
EXTRA=()
while getopts "r:s:x:d:h" o; do
  case $o in
    r) ROOT=$OPTARG ;;
    s) SINCE=$OPTARG ;;
    x) EXTRA+=("$OPTARG") ;;
    d) DEPTH=$OPTARG ;;
    *) sed -n '2,17p' "$0"; exit 2 ;;
  esac
done

if [ -z "$ROOT" ]; then
  ROOT="$(git rev-parse --show-toplevel 2>/dev/null)/.." || ROOT=.
fi
ROOT="$(cd "$ROOT" && pwd)"

repos=()
while IFS= read -r g; do repos+=("$(dirname "$g")"); done \
  < <(fd -H -I -t d '^\.git$' "$ROOT" --max-depth "$DEPTH" --absolute-path 2>/dev/null | sd '/$' '' | sort)

if [ ${#repos[@]} -eq 0 ]; then echo "no git repos under $ROOT"; exit 0; fi

dirty=0
for r in "${repos[@]}"; do
  rel="${r#$ROOT}"; rel="${rel:-/}"
  branch=$(git -C "$r" rev-parse --abbrev-ref HEAD 2>/dev/null || echo '?')
  head=$(git -C "$r" log -1 --format='%h %s' 2>/dev/null || echo 'no commits')
  ab=$(git -C "$r" rev-list --left-right --count '@{u}...HEAD' 2>/dev/null | awk '{printf "behind %s, ahead %s", $1, $2}')
  [ -z "$ab" ] && ab="no upstream"
  out=$(git -C "$r" status --short --untracked-files=all 2>/dev/null)
  n=$(printf '%s' "$out" | rg -c '' || true); n=${n:-0}
  printf '\n== %s  [%s]  %s\n   HEAD %s\n' "$rel" "$branch" "$ab" "$head"
  if [ -z "$out" ]; then echo "   clean"; else dirty=$((dirty+1)); printf '%s\n' "$out" | sd '^' '   '; fi
  if [ -n "$SINCE" ]; then
    # gitignored files touched since SINCE (git status cannot show these)
    ex=(-E .git)
    for o in "${repos[@]}"; do case "$o" in "$r"/*) ex+=(-E "${o#$r/}") ;; esac; done
    ign=$(cd "$r" && fd -H -I -t f --changed-within "$SINCE" "${ex[@]}" . 2>/dev/null | git check-ignore --stdin 2>/dev/null)
    if [ -n "$ign" ]; then echo "   -- ignored but modified since $SINCE:"; printf '%s\n' "$ign" | sd '^' '      '; fi
  fi
done

if [ -n "$SINCE" ] && [ ${#EXTRA[@]} -gt 0 ]; then
  for d in "${EXTRA[@]}"; do
    printf '\n== outside any repo: %s (modified since %s)\n' "$d" "$SINCE"
    fd -H -I -t f --changed-within "$SINCE" . "$d" -E .git 2>/dev/null | sd '^' '   ' || true
  done
fi

printf '\n%d repo(s) scanned, %d with changes\n' "${#repos[@]}" "$dirty"
