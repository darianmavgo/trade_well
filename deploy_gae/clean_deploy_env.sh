#!/usr/bin/env bash
# =============================================================================
# clean_deploy_env.sh: trim the deployed config object to the variables the code uses.
#
# The app's whole configuration is one object, gs://<bucket>/config/env (see config.sh). Over
# time it collects variables nothing reads any more (the Secret Manager era, renamed settings).
# This script
#   1. scans the codebase for every environment variable it reads,
#   2. pulls the live object (through config.sh),
#   3. writes a new env holding only the variables that are needed,
#   4. uploads it back (through config.sh push, which keeps the old object as config/env.prev).
#
#   deploy_gae/clean_deploy_env.sh                 do it: remove unused variables from the live object
#   deploy_gae/clean_deploy_env.sh --dry-run       show what would be removed; change nothing
#   deploy_gae/clean_deploy_env.sh --scan-only     print the variables the code reads; no gcloud
#   deploy_gae/clean_deploy_env.sh --from-file F [--out G]
#                                                  offline: clean a local env file instead of the object.
#                                                  Never uploads; writes the result to G (chmod 600) if given.
#   --keep A,B                                     also keep these variables (comma separated)
#
# What counts as "needed":
#   needed        a Go file (not a test) or a deploy script reads it: os.Getenv / os.LookupEnv /
#                 getEnv* / appenv.Get / a constant named Env* / get_cfg / set_kv / config.sh set.
#   mentioned     the name appears as a string literal in non-test Go code or as a word in the
#                 deploy scripts, but not in a read the scan recognises. Kept as a precaution
#                 (a name built in a list or a map is not a call the scan can see). Listed apart.
#   always kept   TZ, GCS_BUCKET, GOOGLE_CLOUD_PROJECT (config.Bootstrap reads them), anything in
#                 --keep, and any key whose name is not UPPER_SNAKE_CASE (the scan cannot judge it).
# Everything else in the object is removed. Comments and blank lines are dropped, and a variable
# defined twice keeps its last value (the one the app uses).
#
# Safe by design: values are never printed (names only); the working copy is a mode 0700 temp
# folder removed on exit; the old object stays as config/env.prev next to config/env, so
#   gcloud storage cp gs://<bucket>/config/env.prev gs://<bucket>/config/env
# undoes it. A running instance keeps the values it started with, so nothing changes for it until
# it restarts. After the upload the script reads the key names back and checks them.
# The scan is a text search, not a Go parser: a variable read through a name computed at run time
# is invisible to it. Read the "removed" list, or use --dry-run first.
#
# Environment (same as config.sh): GCP_PROJECT_ID, GCS_BUCKET.
# Needs bash, grep, sed, awk and, unless --scan-only/--from-file, gcloud logged in.
# =============================================================================
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/.." && pwd)"

DRY_RUN=0 SCAN_ONLY=0 FROM_FILE="" OUT_FILE="" KEEP_EXTRA=""
usage() { awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; }
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    --scan-only) SCAN_ONLY=1 ;;
    --from-file) FROM_FILE="${2:?--from-file needs a file}"; shift ;;
    --out) OUT_FILE="${2:?--out needs a file}"; shift ;;
    --keep) KEEP_EXTRA="${2:?--keep needs a comma separated list}"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1 (see --help)" >&2; exit 2 ;;
  esac
  shift
done
[ -z "$OUT_FILE" ] || [ -n "$FROM_FILE" ] || { echo "--out is only for --from-file (the live object is uploaded, not written to a file)" >&2; exit 2; }
[ -z "$FROM_FILE" ] || [ -f "$FROM_FILE" ] || { echo "no such file: $FROM_FILE" >&2; exit 2; }

cd "$root"
umask 077
T="$(mktemp -d)"; chmod 700 "$T"; trap 'rm -rf "$T"' EXIT

NAME='[A-Z][A-Z0-9_]+'
GO_DIRS=(pkg cmd)
SH_DIRS=(deploy_gae deploy_live_local deploy_report_local scripts)
GO_FLAGS=(--include='*.go' --exclude='*_test.go' --exclude-dir=vendor)

# --- 1. scan -----------------------------------------------------------------
{
  # a call that reads the environment with a literal name
  grep -rhoE "${GO_FLAGS[@]}" "(Getenv|LookupEnv|getEnv[A-Za-z]*|envOr|env[A-Z][A-Za-z]*|Get|sub)\([[:space:]]*\"$NAME\"" "${GO_DIRS[@]}" \
    | sed -E "s/.*\"($NAME)\"/\1/"
  # a constant or variable named ...Env... that holds a variable name (EnvAllowlist = "STRATEGY_ALLOWLIST")
  grep -rhoE "${GO_FLAGS[@]}" "[A-Za-z_]*[Ee]nv[A-Za-z_]*[[:space:]]*:?=[[:space:]]*\"$NAME\"" "${GO_DIRS[@]}" \
    | sed -E "s/.*\"($NAME)\"/\1/"
  # the deploy scripts that read or write the config object
  grep -rhoE --include='*.sh' "(get_cfg|set_kv \"\\\$WORK\"|config\\.sh set) $NAME" "${SH_DIRS[@]}" | sed -E 's/.* //'
} | grep -Ev '^(KEY|VALUE)$' | sort -u >"$T/needed"

{
  grep -rhoE "${GO_FLAGS[@]}" "\"$NAME\"" "${GO_DIRS[@]}" | tr -d '"'
  grep -rhoE --include='*.sh' --include='*.yaml' "$NAME" "${SH_DIRS[@]}"
} | sort -u >"$T/mentioned"

n_needed="$(wc -l <"$T/needed" | tr -d ' ')"
if [ "$n_needed" -lt 20 ]; then
  echo "the scan found only $n_needed variable(s); expected dozens. Run this from a full checkout (deploy_gae/ next to pkg/ and cmd/). Nothing was changed." >&2
  exit 1
fi

if [ "$SCAN_ONLY" -eq 1 ]; then
  cat "$T/needed"
  echo "# $n_needed variable(s) read by the code and deploy scripts ($(comm -13 "$T/needed" "$T/mentioned" | wc -l | tr -d ' ') more names appear as string literals, not shown)" >&2
  exit 0
fi

# Variables the runtime, the OS or config.Bootstrap supplies; not expected in the object, so never "missing" from it.
RUNTIME_SET="GAE_APPLICATION GAE_ENV GAE_INSTANCE GAE_RUNTIME GAE_SERVICE GAE_VERSION PORT USER SSL_CERT_FILE GCS_BUCKET GOOGLE_CLOUD_PROJECT"
{ printf '%s\n' TZ GCS_BUCKET GOOGLE_CLOUD_PROJECT; echo "$KEEP_EXTRA" | tr ',' '\n' | sed -E 's/[[:space:]]//g'; } | grep -E '^.+$' | sort -u >"$T/keep"

# --- 2. the current object ---------------------------------------------------
cur="$T/current"
if [ -n "$FROM_FILE" ]; then
  cp "$FROM_FILE" "$cur"
else
  "$here/config.sh" pull "$cur" >/dev/null
fi
chmod 600 "$cur"
key_names() { awk -F= '/^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*=/ {gsub(/[[:space:]]/, "", $1); print $1}' "$1" | sort -u; }
key_names "$cur" >"$T/cur_keys"
[ -s "$T/cur_keys" ] || { echo "the current env has no variables; nothing to clean" >&2; exit 1; }

# keys the scan cannot judge: not UPPER_SNAKE_CASE
grep -Ev "^$NAME\$" "$T/cur_keys" | sort -u >"$T/odd" || true
{ cat "$T/needed" "$T/mentioned" "$T/keep" "$T/odd"; } | sort -u >"$T/want"

comm -12 "$T/cur_keys" "$T/want" >"$T/keep_keys"
comm -23 "$T/cur_keys" "$T/want" >"$T/remove_keys"
{ cat "$T/needed" "$T/keep"; } | sort -u >"$T/strong"
comm -12 "$T/keep_keys" "$T/strong" >"$T/kept_needed"
comm -12 "$T/keep_keys" "$T/odd" >"$T/kept_odd"
comm -23 "$T/keep_keys" "$T/strong" | comm -23 - "$T/odd" >"$T/kept_precaution"
# read by the code but not in the object (names the runtime supplies left out)
for v in $RUNTIME_SET; do echo "$v"; done | sort -u >"$T/runtime"
comm -23 "$T/needed" "$T/cur_keys" | comm -23 - "$T/runtime" >"$T/missing"

# --- 3. the new env ----------------------------------------------------------
# Keep the wanted keys, one line each (a variable defined twice keeps its last line, as the app reads it).
new="$T/new"
awk -v keys="$T/keep_keys" '
  function keyof(line,   k) { if (match(line, /^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*=/)) { k = substr(line, RSTART, RLENGTH - 1); gsub(/[[:space:]]/, "", k); return k } return "" }
  BEGIN { while ((getline k < keys) > 0) ok[k] = 1 }
  NR == FNR { k = keyof($0); if (k != "") last[k] = NR; next }
  { k = keyof($0); if (k != "" && ok[k] && last[k] == FNR) print }
' "$cur" "$cur" >"$new"
chmod 600 "$new"
key_names "$new" >"$T/new_keys"
dup_lines=$(( $(grep -cE '^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*=' "$cur") - $(wc -l <"$T/cur_keys" | tr -d ' ') ))

show() { # TITLE FILE
  local n; n="$(wc -l <"$2" | tr -d ' ')"
  printf '%s (%s)\n' "$1" "$n"
  [ "$n" -eq 0 ] || sed 's/^/  /' "$2"
}
echo "scanned ${GO_DIRS[*]} (Go) and ${SH_DIRS[*]} (scripts): $n_needed variable(s) read"
echo "object has $(wc -l <"$T/cur_keys" | tr -d ' ') variable(s). Names only, no values."
echo
show "keep: needed" "$T/kept_needed"
show "keep: only mentioned as a string (precaution)" "$T/kept_precaution"
[ ! -s "$T/kept_odd" ] || show "keep: not UPPER_SNAKE_CASE (not judged)" "$T/kept_odd"
echo
show "REMOVE: nothing in the code or deploy scripts refers to these" "$T/remove_keys"
echo
show "read by the code but not set in the object (optional or has a default)" "$T/missing"

if [ ! -s "$T/remove_keys" ] && [ "$dup_lines" -le 0 ]; then
  echo
  echo "already clean: nothing to remove."
  exit 0
fi
[ "$dup_lines" -le 0 ] || echo "also: $dup_lines duplicate definition(s) collapse to the last value."
[ -s "$new" ] || { echo "the new env would be empty; refusing" >&2; exit 1; }

# --- 4. write it back --------------------------------------------------------
if [ -n "$FROM_FILE" ]; then
  echo
  if [ -n "$OUT_FILE" ]; then
    cp "$new" "$OUT_FILE"; chmod 600 "$OUT_FILE"
    echo "wrote $OUT_FILE: $(wc -l <"$T/new_keys" | tr -d ' ') variable(s). Nothing uploaded (--from-file)."
  else
    echo "offline run (--from-file): pass --out FILE to write the cleaned env. Nothing uploaded."
  fi
  exit 0
fi
if [ "$DRY_RUN" -eq 1 ]; then
  echo
  echo "dry run: would upload $(wc -l <"$T/new_keys" | tr -d ' ') variable(s) and remove $(wc -l <"$T/remove_keys" | tr -d ' '). Nothing was changed."
  exit 0
fi

echo
"$here/config.sh" push "$new"
# read the names back from the live object and check them
"$here/config.sh" keys | sort -u >"$T/live_keys"
if ! cmp -s "$T/live_keys" "$T/new_keys"; then
  echo "WARNING: the live object's variable names differ from what was uploaded. The previous object is config/env.prev:" >&2
  diff "$T/new_keys" "$T/live_keys" >&2 || true
  exit 1
fi
echo "verified: the live object now holds $(wc -l <"$T/live_keys" | tr -d ' ') variable(s), removed $(wc -l <"$T/remove_keys" | tr -d ' ')."
echo "undo: gcloud storage cp gs://${GCS_BUCKET:-<bucket>}/config/env.prev gs://${GCS_BUCKET:-<bucket>}/config/env"
echo "A running instance keeps the values it started with; nothing it already loaded changed."
