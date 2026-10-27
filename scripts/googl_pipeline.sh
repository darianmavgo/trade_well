#!/usr/bin/env bash
# The GOOGL pipeline. All the logic is the pipeline command (pkg/pipeline), which
# runs every stage in one numbered run folder under data/reports and keeps to the
# symbols and strategies it was started with. Extra arguments go to it, for example:
#   scripts/googl_pipeline.sh -skip-network
#   scripts/googl_pipeline.sh -run-id 17        # resume run 17
set -euo pipefail
cd "$(dirname "$0")/.."
make build >/dev/null
exec ./bin/pipeline -symbol GOOGL "$@"
