#!/usr/bin/env bash
# =============================================================================
# setup_oauth2.sh: do everything for IBKR OAuth 2.0 that can be done from this side, and
# print exactly what only IBKR (or you, in their portal) can do.
#
#   ./setup_oauth2.sh [--client-id ID] [--credential IB_USERNAME] [--ip ADDR]
#                     [--key-id KID] [--scope SCOPE] [--upload] [--verify] [--force]
#
# What it does
#   1. Generates an RSA-2048 key pair in keys/ (private key chmod 600, git-ignored).
#      Existing keys are kept unless --force.
#   2. Prints the PUBLIC key (PEM), its JWK and its SHA-256 thumbprint: paste one of them
#      where IBKR asks you to register a public key. The private key never leaves this
#      machine except with --upload.
#   3. Adds the client settings to ./.env without touching anything already there
#      (IBKR_PASSWORD is never read or written): IBKR_PRIVATE_KEY_PATH, IBKR_KEY_ID and,
#      when given, IBKR_CLIENT_ID / IBKR_CREDENTIAL / IBKR_IP / IBKR_SCOPE.
#   4. --upload stores the private key, base64-encoded on one line, as IBKR_PRIVATE_KEY in
#      the shared GCS config object (../trade_orchestrator/deploy_gae/config.sh set), so a
#      deployed service can sign without your laptop. Skip it if the key must stay local.
#   5. --verify builds bin/ibkr and asks IBKR for a token (`ibkr auth`), which is the first
#      real proof that IBKR accepts your client id and key.
#
# What only IBKR can do (printed at the end)
#   * approve OAuth for your account and issue the client id;
#   * register the public key printed above against that client id;
#   * for SSO sessions: register the IP address the requests come from.
# =============================================================================
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KEY_DIR="$HERE/keys"
PRIV="$KEY_DIR/ibkr_oauth2_private.pem"
PUB="$KEY_DIR/ibkr_oauth2_public.pem"
ENV_FILE="$HERE/.env"
CONFIG_SH="$HERE/../trade_orchestrator/deploy_gae/config.sh"

CLIENT_ID=""; CREDENTIAL=""; IP=""; KEY_ID=""; SCOPE=""; UPLOAD=0; VERIFY=0; FORCE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --client-id) CLIENT_ID="${2:?--client-id needs a value}"; shift 2 ;;
    --credential) CREDENTIAL="${2:?--credential needs a value}"; shift 2 ;;
    --ip) IP="${2:?--ip needs a value}"; shift 2 ;;
    --key-id) KEY_ID="${2:?--key-id needs a value}"; shift 2 ;;
    --scope) SCOPE="${2:?--scope needs a value}"; shift 2 ;;
    --upload) UPLOAD=1; shift ;;
    --verify) VERIFY=1; shift ;;
    --force) FORCE=1; shift ;;
    -h|--help) awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; exit 0 ;;
    *) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
  esac
done

command -v openssl >/dev/null || { echo "openssl not found" >&2; exit 1; }

# --- 1. keys -------------------------------------------------------------------
mkdir -p "$KEY_DIR"; chmod 700 "$KEY_DIR"
if [ -s "$PRIV" ] && [ "$FORCE" -ne 1 ]; then
  echo "keys: keeping the existing private key ($PRIV); --force makes a new pair"
else
  umask 077
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$PRIV" 2>/dev/null
  chmod 600 "$PRIV"
  echo "keys: generated $PRIV (private, chmod 600)"
fi
openssl pkey -in "$PRIV" -pubout -out "$PUB" 2>/dev/null

# --- 2. public material to register with IBKR ----------------------------------
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
MOD_HEX="$(openssl rsa -in "$PRIV" -noout -modulus 2>/dev/null | sed 's/^Modulus=//')"
N="$(printf '%s' "$MOD_HEX" | xxd -r -p | b64url)"
E="AQAB" # public exponent 65537
THUMB="$(printf '{"e":"%s","kty":"RSA","n":"%s"}' "$E" "$N" | openssl dgst -sha256 -binary | b64url)"
[ -n "$KEY_ID" ] || KEY_ID="$THUMB"

echo
echo "== PUBLIC KEY (PEM) to register with IBKR =="
cat "$PUB"
echo "== the same key as a JWK =="
printf '{"kty":"RSA","alg":"RS256","use":"sig","kid":"%s","n":"%s","e":"%s"}\n' "$KEY_ID" "$N" "$E"
echo "== key id / SHA-256 thumbprint: $KEY_ID"

# --- 3. .env (add only what is missing; never touch existing lines) ------------
touch "$ENV_FILE"; chmod 600 "$ENV_FILE"
add_env() { # KEY VALUE: append KEY=VALUE unless KEY is already set in .env
  if grep -q "^[[:space:]]*$1[[:space:]]*=" "$ENV_FILE"; then
    echo ".env: $1 already set; left alone"
  else
    [ -s "$ENV_FILE" ] && [ -n "$(tail -c1 "$ENV_FILE")" ] && printf '\n' >>"$ENV_FILE"
    printf '%s=%s\n' "$1" "$2" >>"$ENV_FILE"
    echo ".env: added $1"
  fi
}
add_env IBKR_PRIVATE_KEY_PATH "$PRIV"
add_env IBKR_KEY_ID "$KEY_ID"
[ -z "$CLIENT_ID" ] || add_env IBKR_CLIENT_ID "$CLIENT_ID"
[ -z "$CREDENTIAL" ] || add_env IBKR_CREDENTIAL "$CREDENTIAL"
[ -z "$IP" ] || add_env IBKR_IP "$IP"
[ -z "$SCOPE" ] || add_env IBKR_SCOPE "$SCOPE"

# --- 4. optional: private key into the shared GCS config -----------------------
if [ "$UPLOAD" -eq 1 ]; then
  [ -x "$CONFIG_SH" ] || { echo "cannot find $CONFIG_SH" >&2; exit 1; }
  "$CONFIG_SH" set "IBKR_PRIVATE_KEY=$(openssl base64 -A <"$PRIV")" >/dev/null
  echo "gcs: IBKR_PRIVATE_KEY stored (base64) in the shared config object"
fi

# --- 5. optional: prove IBKR accepts it ----------------------------------------
if [ "$VERIFY" -eq 1 ]; then
  (cd "$HERE" && go build -o bin/ibkr ./cmd/ibkr && ./bin/ibkr auth)
fi

cat <<EOF

================================================================================
 Still needed from IBKR (this script cannot do these)
================================================================================
 1. Ask IBKR for OAuth 2.0 / Web API access for your account (Client Portal >
    Settings > API, or IBKR support). Individual accounts may not be eligible: if
    they say no, the local Client Portal Gateway is the only route.
 2. Register the PUBLIC key above against the client id they issue you.
 3. Put the client id in .env:   IBKR_CLIENT_ID=<id>   (or re-run with --client-id)
    For SSO sessions also: IBKR_CREDENTIAL=<your IB username>  IBKR_IP=<registered IP>
 4. Prove it:   ./setup_oauth2.sh --verify      (runs  bin/ibkr auth  in OAuth mode)
 5. Then read your positions with no gateway:  ./bin/ibkr account_state

 Files: private key $PRIV (never commit or share it; keys/ is git-ignored),
        public key $PUB
================================================================================
EOF
