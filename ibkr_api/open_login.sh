#!/usr/bin/env bash
# open_login.sh: start the gateway if needed, open Chrome at its login page, and wait until
# you are logged in (`ibkr gateway login`). Chrome warns about the self-signed certificate:
# Advanced > Proceed.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$HERE" && go build -o bin/ibkr ./cmd/ibkr
./bin/ibkr gateway setup   # idempotent: files, Java, port, process
exec ./bin/ibkr gateway login
