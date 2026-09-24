#!/usr/bin/env bash
# git_sync.sh: git add -A, commit and push across the three trade_well repos.
#
# Repos are handled in dependency order (schwaber, backtestgosqlite, then
# trade_orchestrator), each on its own current branch. A repo with no changes is
# skipped. A failure in one repo is reported and the others still run.
#
# Usage:
#   ./git_sync.sh                          interactive: shows changes, asks before each commit
#   ./git_sync.sh -m "message"             one commit message for every repo
#   ./git_sync.sh -n                       dry run: show what would happen, change nothing
#   ./git_sync.sh -y                       do not ask for confirmation
#   ./git_sync.sh -t                       run go build, vet and test in each repo first; skip a repo that fails
#   ./git_sync.sh -C                       commit only, do not push
#   ./git_sync.sh schwaber trade_orchestrator    only the named repos
#
# Safety (never force-pushes, never rebases, never touches other branches):
#   - refuses to commit a repo that has staged secrets or databases (.env, token
#     files, *.db, keys) or any file over 5 MB, and unstages it instead
#   - makes no network call unless there is something to push; a push that would
#     overwrite the remote is refused by git and reported (never forced)
#   - CLAUDE.md, *.html reports and this script live above the repos and are in
#     no git repo, so they are not committed by this script

set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ALL_REPOS=(schwaber backtestgosqlite trade_orchestrator)
MAX_BYTES=$((5 * 1024 * 1024))
SECRET_RE='(^|/)(\.env(\.[^/]*)?|tokens?\.json|schwab_token\.json|credentials\.json|[^/]*\.token|[^/]*\.pem|[^/]*\.key|[^/]*\.db|[^/]*\.db-shm|[^/]*\.db-wal|[^/]*\.sqlite[^/]*)$'

# Never hang on the network: no credential prompts, and SSH gives up fast instead
# of waiting on a passphrase, host-key or dead connection.
export GIT_TERMINAL_PROMPT=0
export GIT_SSH_COMMAND="${GIT_SSH_COMMAND:-ssh} -o ConnectTimeout=15 -o ServerAliveInterval=5 -o ServerAliveCountMax=3"

MSG=""
DRY=0
YES=0
CHECK=0
NOPUSH=0
REPOS=()

while [ $# -gt 0 ]; do
  case "$1" in
    -m) [ $# -ge 2 ] || { echo "-m needs a message" >&2; exit 2; }; MSG="$2"; shift 2 ;;
    -n) DRY=1; shift ;;
    -y) YES=1; shift ;;
    -t) CHECK=1; shift ;;
    -C) NOPUSH=1; shift ;;
    -h|--help) sed -n '2,24p' "$0"; exit 0 ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) REPOS+=("$1"); shift ;;
  esac
done
[ ${#REPOS[@]} -eq 0 ] && REPOS=("${ALL_REPOS[@]}")

if [ -t 1 ]; then B=$'\033[1m'; R=$'\033[31m'; G=$'\033[32m'; Y=$'\033[33m'; X=$'\033[0m'; else B=""; R=""; G=""; Y=""; X=""; fi

RESULTS=()
FAILED=0
record() { RESULTS+=("$1|$2|$3"); }

sync_repo() {
  local name="$1" dir="$ROOT/$1"
  echo
  echo "${B}=== $name ===${X}"

  if [ ! -d "$dir" ]; then record "$name" "FAILED" "directory not found"; FAILED=1; return; fi
  if ! git -C "$dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    record "$name" "FAILED" "not a git repo"; FAILED=1; return
  fi

  local branch
  branch="$(git -C "$dir" branch --show-current)"
  if [ -z "$branch" ]; then record "$name" "FAILED" "detached HEAD"; FAILED=1; return; fi
  echo "branch: $branch   remote: $(git -C "$dir" remote get-url origin 2>/dev/null || echo none)"

  if [ -z "$(git -C "$dir" status --porcelain)" ]; then
    echo "${G}clean, nothing to commit${X}"
    push_if_ahead "$name" "$dir" "$branch" "clean"
    return
  fi

  git -C "$dir" status --short

  if [ "$CHECK" -eq 1 ]; then
    echo "checking: go build, vet, test ..."
    if ! (cd "$dir" && go build ./... && go vet ./... && go test ./... >/dev/null); then
      echo "${R}build/vet/test failed; not committing $name${X}"
      record "$name" "SKIPPED" "go build/vet/test failed"; FAILED=1; return
    fi
    echo "${G}checks passed${X}"
  fi

  if [ "$DRY" -eq 1 ]; then
    echo "${Y}[dry run] would: git add -A, commit, push origin $branch${X}"
    record "$name" "DRY-RUN" "$(git -C "$dir" status --porcelain | wc -l | tr -d ' ') changed path(s)"
    return
  fi

  local msg="$MSG"
  [ -z "$msg" ] && msg="Update $name ($(date '+%Y-%m-%d %H:%M'))"

  if [ "$YES" -eq 0 ]; then
    printf "commit and %s %s with message \"%s\"? [y/N] " "$([ "$NOPUSH" -eq 1 ] && echo "keep local" || echo push)" "$name" "$msg"
    read -r ans
    case "$ans" in y|Y|yes) ;; *) record "$name" "SKIPPED" "declined"; return ;; esac
  fi

  git -C "$dir" add -A || { record "$name" "FAILED" "git add failed"; FAILED=1; return; }

  # Guard: nothing secret or huge may be staged, even though .gitignore should stop it.
  local bad big f
  bad="$(git -C "$dir" diff --cached --name-only --diff-filter=AMR | grep -E "$SECRET_RE" | grep -Ev '\.env\.example$' || true)"
  big=""
  while IFS= read -r f; do
    [ -n "$f" ] && [ -f "$dir/$f" ] && [ "$(wc -c < "$dir/$f")" -gt "$MAX_BYTES" ] && big="$big$f"$'\n'
  done < <(git -C "$dir" diff --cached --name-only --diff-filter=AMR)
  if [ -n "$bad" ] || [ -n "$big" ]; then
    echo "${R}BLOCKED: refusing to commit $name${X}"
    [ -n "$bad" ] && { echo "secret/database-looking files staged:"; echo "$bad" | sed 's/^/  /'; }
    [ -n "$big" ] && { echo "files over 5 MB staged:"; printf "%s" "$big" | sed 's/^/  /'; }
    git -C "$dir" reset -q
    record "$name" "BLOCKED" "unsafe files staged (unstaged again)"; FAILED=1; return
  fi

  if ! git -C "$dir" commit -q -m "$msg"; then
    record "$name" "FAILED" "git commit failed"; FAILED=1; return
  fi
  echo "${G}committed:${X} $(git -C "$dir" log --oneline -1)"

  if [ "$NOPUSH" -eq 1 ]; then record "$name" "COMMITTED" "not pushed (-C)"; return; fi
  push_if_ahead "$name" "$dir" "$branch" "committed"
}

# push_if_ahead pushes only when local commits are not on origin, judged from the
# local tracking ref, so a clean, up-to-date repo makes no network call at all.
# There is no pre-fetch: git push itself refuses a non-fast-forward, and that
# refusal is reported. The tracking ref is only as fresh as your last fetch/push.
push_if_ahead() {
  local name="$1" dir="$2" branch="$3" state="$4"
  if [ "$NOPUSH" -eq 1 ]; then
    [ "$state" = "clean" ] && record "$name" "CLEAN" "nothing to do"
    return
  fi
  if ! git -C "$dir" remote get-url origin >/dev/null 2>&1; then
    if [ "$state" = "clean" ]; then record "$name" "CLEAN" "no remote 'origin'"; else record "$name" "COMMITTED" "no remote 'origin', not pushed"; fi
    return
  fi

  local upstream="origin/$branch" ahead
  if git -C "$dir" rev-parse --verify -q "$upstream" >/dev/null; then
    ahead="$(git -C "$dir" rev-list --count "$upstream..HEAD")"
    if [ "$ahead" -eq 0 ]; then
      record "$name" "CLEAN" "nothing to push (vs local $upstream)"; return
    fi
    if [ "$DRY" -eq 1 ]; then echo "[dry run] would push $ahead commit(s)"; record "$name" "DRY-RUN" "$ahead commit(s) to push"; return; fi
    echo "pushing $ahead commit(s) to $upstream ..."
    if git -C "$dir" push origin "$branch"; then
      record "$name" "PUSHED" "$ahead commit(s) to $upstream"
    else
      record "$name" "FAILED" "$state; push failed or rejected (behind remote? pull by hand)"; FAILED=1
    fi
  else
    if [ "$DRY" -eq 1 ]; then echo "[dry run] would push new branch $branch"; record "$name" "DRY-RUN" "new branch"; return; fi
    echo "pushing new branch $branch ..."
    if git -C "$dir" push -u origin "$branch"; then
      record "$name" "PUSHED" "new branch $branch"
    else
      record "$name" "FAILED" "$state; push failed"; FAILED=1
    fi
  fi
}

for r in "${REPOS[@]}"; do sync_repo "$r"; done

echo
echo "${B}=== summary ===${X}"
printf "%-20s %-10s %s\n" "repo" "result" "detail"
for line in "${RESULTS[@]}"; do
  IFS='|' read -r n s d <<<"$line"
  printf "%-20s %-10s %s\n" "$n" "$s" "$d"
done
exit "$FAILED"
