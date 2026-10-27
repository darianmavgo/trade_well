#!/usr/bin/env bash
# =============================================================================
# config.sh: the app's whole configuration is ONE object,
#   gs://trade-orchestrator-data-<project>/config/env      (KEY=VALUE lines, .env format)
# It replaces Secret Manager (`schwab-env`), app.yaml env_variables and .env. Every
# binary reads it at startup (schwaber config.Bootstrap); an environment variable that
# is already set (a shell override, GAE_*) wins over it. A running
# instance keeps the values it started with: redeploy, or wait for the idle instance to
# stop, after changing it.
#
#   deploy_gae/config.sh migrate [--force]   build the object from the old sources
#   deploy_gae/config.sh keys                list variable NAMES only (never values)
#   deploy_gae/config.sh list                list every variable; values shown only for the
#                                            non-secret ones (STRATEGY_ALLOWLIST, TZ, APP_*, ...)
#   deploy_gae/config.sh edit                open it in $EDITOR, upload if changed
#   deploy_gae/config.sh set KEY=VALUE ...   set/replace variables
#   deploy_gae/config.sh unset KEY ...       remove variables
#   deploy_gae/config.sh pull FILE           download (FILE is created chmod 600)
#   deploy_gae/config.sh push FILE           upload FILE (previous copy kept as config/env.prev)
#
# migrate: merges the Secret Manager secret `schwab-env` (if it still exists) with the
# non-secret settings that used to be app.yaml env_variables; app.yaml values won, as
# they did at run time, so a stale STRATEGY_ALLOWLIST in the secret is replaced. It
# refuses to overwrite an existing object unless --force. Values are never printed.
#
# Needs `gcloud auth login` (this script) and, for the app locally,
# `gcloud auth application-default login`. Keep the bucket private: it holds Schwab
# credentials. Enforce it with:
#   gcloud storage buckets update gs://<bucket> --public-access-prevention
# =============================================================================
set -euo pipefail

PROJECT="${GCP_PROJECT_ID:-trade-orchestrator-508722}"
BUCKET="${GCS_BUCKET:-trade-orchestrator-data-$PROJECT}"
OBJ="gs://$BUCKET/config/env"
PREV="gs://$BUCKET/config/env.prev"

if ! command -v gcloud >/dev/null 2>&1 && [ -x "$HOME/google-cloud-sdk/bin/gcloud" ]; then
  export PATH="$HOME/google-cloud-sdk/bin:$PATH"
fi
command -v gcloud >/dev/null 2>&1 || { echo "gcloud not found" >&2; exit 1; }

exists() { gcloud storage ls "$1" --project="$PROJECT" >/dev/null 2>&1; }
fetch() { gcloud storage cat "$OBJ" --project="$PROJECT"; }
upload() { # FILE: keep the previous object, then replace
  if exists "$OBJ"; then gcloud storage cp "$OBJ" "$PREV" --project="$PROJECT" >/dev/null; fi
  gcloud storage cp "$1" "$OBJ" --project="$PROJECT" --content-type=text/plain >/dev/null
}
key_names() { awk -F= '/^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*=/ {gsub(/[[:space:]]/, "", $1); print $1}' | sort -u; }
# set_kv FILE KEY VALUE: replace or append one KEY=VALUE line
set_kv() {
  local f="$1" k="$2" v="$3" tmp; tmp="$(mktemp)"
  grep -v "^[[:space:]]*$k=" "$f" >"$tmp" || true
  [ -s "$tmp" ] && [ -n "$(tail -c1 "$tmp")" ] && printf '\n' >>"$tmp"
  printf '%s=%s\n' "$k" "$v" >>"$tmp"
  cat "$tmp" >"$f"; rm -f "$tmp"
}

cmd="${1:-}"; [ $# -gt 0 ] && shift
case "$cmd" in
  migrate)
    FORCE=0; [ "${1:-}" = "--force" ] && FORCE=1
    if exists "$OBJ" && [ "$FORCE" -ne 1 ]; then
      echo "$OBJ already exists; not overwriting. Use 'migrate --force' to rebuild it (the old one is kept as config/env.prev)." >&2
      exit 1
    fi
    WORK="$(mktemp)"; chmod 600 "$WORK"; trap 'rm -f "$WORK"' EXIT
    if gcloud secrets describe schwab-env --project="$PROJECT" >/dev/null 2>&1; then
      gcloud secrets versions access latest --secret=schwab-env --project="$PROJECT" >"$WORK"
      echo "read the secret schwab-env: $(key_names <"$WORK" | wc -l | tr -d ' ') variable(s)"
    else
      echo "no secret schwab-env: starting from the app.yaml settings only"
    fi
    # The settings that used to be app.yaml env_variables (they beat the secret at run time).
    set_kv "$WORK" TZ America/New_York
    set_kv "$WORK" APP_FOLDER /tmp/trade_orchestrator
    set_kv "$WORK" MARKET_DB /tmp/trade_orchestrator/market_history.db
    set_kv "$WORK" SCHWAB_TOKEN_PATH /tmp/trade_orchestrator/token.json
    set_kv "$WORK" SCHWAB_CALLBACK_URL https://mavgo.com/schwabcallback
    # Every entry must resolve in this build, or every job stops. A strategy that is a database row
    # (streak, markov, tree, hold) must be in pkg/orchestrator/strategies/deploy/strategies.db, made with
    # `strategy export -models-out` in backtestgosqlite (which also bundles its trained markov/tree models). mara_tree and pdd_tree are not bundled yet.
    # This line only seeds a NEW config object. The live value is in the object itself:
    #   deploy_gae/config.sh set STRATEGY_ALLOWLIST="<same value>"
    set_kv "$WORK" STRATEGY_ALLOWLIST "streak-voo-buy-tecl+markov_model_amd+markov_model_lite+markov_model_mull,tsll-daily-one-share,rotation-2x-sector-pairs-daily-limit95-tp3-sl5-hold2-pos20"
    upload "$WORK"
    echo "wrote $OBJ: $(key_names <"$WORK" | wc -l | tr -d ' ') variable(s):"
    key_names <"$WORK" | sed 's/^/  /'
    echo "Now deploy. After a deploy that runs on this object, the schwab-env secret is unused and can be deleted."
    ;;
  keys)
    fetch | key_names ;;
  list)
    # Values are printed only for the keys in SHOW_KEYS; the object also holds credentials.
    SHOW_KEYS=" STRATEGY_ALLOWLIST TZ APP_FOLDER MARKET_DB SCHWAB_TOKEN_PATH SCHWAB_CALLBACK_URL "
    fetch | while IFS= read -r line; do
      k="${line%%=*}"; k="${k//[[:space:]]/}"
      case "$line" in [[:space:]]*[A-Za-z_]*=*|[A-Za-z_]*=*) ;; *) continue ;; esac
      case "$SHOW_KEYS" in *" $k "*) printf '%s=%s\n' "$k" "${line#*=}" ;; *) printf '%s=(hidden)\n' "$k" ;; esac
    done | sort ;;
  edit)
    WORK="$(mktemp)"; chmod 600 "$WORK"; trap 'rm -f "$WORK" "$WORK.orig"' EXIT
    fetch >"$WORK"; cp "$WORK" "$WORK.orig"
    "${EDITOR:-vi}" "$WORK"
    if cmp -s "$WORK" "$WORK.orig"; then echo "unchanged"; else upload "$WORK"; echo "uploaded $OBJ"; fi ;;
  set)
    [ $# -gt 0 ] || { echo "usage: config.sh set KEY=VALUE ..." >&2; exit 2; }
    WORK="$(mktemp)"; chmod 600 "$WORK"; trap 'rm -f "$WORK"' EXIT
    fetch >"$WORK"
    for kv in "$@"; do
      case "$kv" in [A-Za-z_]*=*) set_kv "$WORK" "${kv%%=*}" "${kv#*=}"; echo "set ${kv%%=*}" ;; *) echo "bad argument (want KEY=VALUE): ${kv%%=*}" >&2; exit 2 ;; esac
    done
    upload "$WORK" ;;
  unset)
    [ $# -gt 0 ] || { echo "usage: config.sh unset KEY ..." >&2; exit 2; }
    WORK="$(mktemp)"; chmod 600 "$WORK"; trap 'rm -f "$WORK"' EXIT
    fetch >"$WORK"
    for k in "$@"; do grep -v "^[[:space:]]*$k=" "$WORK" >"$WORK.n" || true; cat "$WORK.n" >"$WORK"; rm -f "$WORK.n"; echo "unset $k"; done
    upload "$WORK" ;;
  pull)
    F="${1:?usage: config.sh pull FILE}"; umask 077; fetch >"$F"; chmod 600 "$F"; echo "wrote $F" ;;
  push)
    F="${1:?usage: config.sh push FILE}"; [ -s "$F" ] || { echo "$F is empty or missing" >&2; exit 1; }
    upload "$F"; echo "uploaded $OBJ" ;;
  ""|-h|--help) awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0" ;;
  *) echo "unknown command: $cmd (see --help)" >&2; exit 2 ;;
esac
