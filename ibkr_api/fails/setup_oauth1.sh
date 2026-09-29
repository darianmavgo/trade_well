#!/usr/bin/env bash
# =============================================================================
# setup_oauth1.sh: prepare IBKR OAuth 1.0a (the Web API's self-service OAuth) from this
# side. IBKR's self-service portal asks you to upload public keys and a Diffie-Hellman
# parameter file, then gives you a consumer key, an access token and an (encrypted)
# access-token secret.
#
#   ./setup_oauth1.sh [--consumer-key KEY] [--access-token TOK] [--token-secret SECRET]
#                     [--realm test_realm] [--verify] [--force]
#
# 1. Generates in oauth1_keys/ (chmod 600, git-ignored): private_signature.pem,
#    private_encryption.pem (2048-bit RSA) and dhparam.pem (2048-bit DH; can take a
#    minute), plus the matching public_*.pem you upload to IBKR.
# 2. Adds the IBKR_OAUTH1_* settings to ./.env without touching existing lines.
# 3. --verify builds bin/ibkr and runs `ibkr auth`: it negotiates the live session token
#    with IBKR and starts the brokerage session. That call is the first real proof.
#
# Only you can: open the self-service OAuth page for your account, upload
# public_signature.pem, public_encryption.pem and dhparam.pem, and copy back the
# consumer key, access token and access-token secret (pass them as flags, or edit .env).
# =============================================================================
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
D="$HERE/oauth1_keys"; ENV_FILE="$HERE/.env"
CK=""; AT=""; TS=""; REALM=""; VERIFY=0; FORCE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --consumer-key) CK="${2:?}"; shift 2 ;;
    --access-token) AT="${2:?}"; shift 2 ;;
    --token-secret) TS="${2:?}"; shift 2 ;;
    --realm) REALM="${2:?}"; shift 2 ;;
    --verify) VERIFY=1; shift ;;
    --force) FORCE=1; shift ;;
    -h|--help) awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
command -v openssl >/dev/null || { echo "openssl not found" >&2; exit 1; }
mkdir -p "$D"; chmod 700 "$D"; umask 077
gen_rsa() { # NAME
  if [ -s "$D/private_$1.pem" ] && [ "$FORCE" -ne 1 ]; then echo "keys: keeping $D/private_$1.pem"; else
    openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$D/private_$1.pem" 2>/dev/null; echo "keys: generated private_$1.pem"; fi
  openssl pkey -in "$D/private_$1.pem" -pubout -out "$D/public_$1.pem" 2>/dev/null
}
gen_rsa signature; gen_rsa encryption
if [ -s "$D/dhparam.pem" ] && [ "$FORCE" -ne 1 ]; then echo "keys: keeping $D/dhparam.pem"; else
  echo "generating dhparam.pem (2048-bit, may take a minute)..."; openssl dhparam -out "$D/dhparam.pem" 2048 2>/dev/null; fi
chmod 600 "$D"/*.pem

touch "$ENV_FILE"; chmod 600 "$ENV_FILE"
add_env() { if grep -q "^[[:space:]]*$1[[:space:]]*=" "$ENV_FILE"; then echo ".env: $1 already set; left alone"; else
  [ -s "$ENV_FILE" ] && [ -n "$(tail -c1 "$ENV_FILE")" ] && printf '\n' >>"$ENV_FILE"; printf '%s=%s\n' "$1" "$2" >>"$ENV_FILE"; echo ".env: added $1"; fi; }
add_env IBKR_OAUTH1_SIGNATURE_KEY_PATH "$D/private_signature.pem"
add_env IBKR_OAUTH1_ENCRYPTION_KEY_PATH "$D/private_encryption.pem"
add_env IBKR_OAUTH1_DHPARAM_PATH "$D/dhparam.pem"
[ -z "$CK" ] || add_env IBKR_OAUTH1_CONSUMER_KEY "$CK"
[ -z "$AT" ] || add_env IBKR_OAUTH1_ACCESS_TOKEN "$AT"
[ -z "$TS" ] || add_env IBKR_OAUTH1_ACCESS_TOKEN_SECRET "$TS"
[ -z "$REALM" ] || add_env IBKR_OAUTH1_REALM "$REALM"

if [ "$VERIFY" -eq 1 ]; then (cd "$HERE" && go build -o bin/ibkr ./cmd/ibkr && ./bin/ibkr auth); fi
cat <<EOF

Upload to IBKR's self-service OAuth page (yours to do):
  $D/public_signature.pem     (signature public key)
  $D/public_encryption.pem    (encryption public key)
  $D/dhparam.pem              (Diffie-Hellman parameters)
Then copy back the consumer key, access token and access-token secret:
  ./setup_oauth1.sh --consumer-key <KEY> --access-token <TOKEN> --token-secret <SECRET> --verify
  (test consumers use --realm test_realm)
Then: ./bin/ibkr account_state
EOF
